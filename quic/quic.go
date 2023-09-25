package quic

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/quic-go/quic-go"
	"golang.org/x/sync/errgroup"

	"github.com/hcholab/sfkit-proxy/ice"
	"github.com/hcholab/sfkit-proxy/mpc"
	"github.com/hcholab/sfkit-proxy/util"
)

type tlsConfsGetter func(context.Context, mpc.PID, []net.PacketConn) (<-chan *ice.TLSConf, io.Closer, error)

type Service struct {
	mpc         *mpc.Config
	qConf       *quic.Config
	errs        *errgroup.Group
	getTLSConfs tlsConfsGetter
}

const retryMs = 1000

func NewService(mpcConf *mpc.Config, tcg tlsConfsGetter, errs *errgroup.Group) (s *Service, err error) {
	qc := &quic.Config{
		KeepAlivePeriod: 15 * time.Second,
	}
	s = &Service{mpcConf, qc, errs, tcg}
	slog.Debug("Started QUIC service")
	return
}

// GetConns establishes a QUIC connection stream with a peer,
// and returns a *conn.Conn channel, which allows the client
// to subscribe to changes in the connection.
func (s *Service) GetConns(ctx context.Context, peerPID mpc.PID) (_ <-chan net.Conn, err error) {
	slog.Debug("Getting connection for", "peerPID", peerPID)

	trs, err := listenUDP()
	if err != nil {
		slog.Error(err.Error())
		return
	}

	rpcs := make([]net.PacketConn, 0, len(trs))
	for _, tr := range trs {
		rpcs = append(rpcs, newRawPacketConn(ctx, tr))
	}

	tcs, c, err := s.getTLSConfs(ctx, peerPID, rpcs)
	if err != nil {
		return
	}

	conns := make(chan net.Conn, s.mpc.Threads)
	s.errs.Go(func() (err error) {
		for _, tr := range trs {
			defer util.Cleanup(&err, tr.Close)
		}
		defer util.Cleanup(&err, c.Close)

		err = util.Retry(ctx, func() (err error) {
			select {
			case tc := <-tcs:
				tr, ok := trs[tc.LocalAddr.String()]
				if !ok {
					err = util.Permanent(fmt.Errorf("no transport for %s", tc.LocalAddr))
					return
				}
				slog.Debug("Using TLS config for", "localAddr", tc.LocalAddr, "peerPID", peerPID)

				if err = util.Retry(ctx, func() error {
					if s.mpc.IsClient(peerPID) {
						return s.handleClient(ctx, tr, tc.Config, peerPID, tc.RemoteAddr, conns)
					} else {
						return s.handleServer(ctx, tr, tc.Config, peerPID, conns)
					}
				})(); err != nil {
					slog.Error(err.Error())
					return
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			return
		})()
		return
	})
	return conns, nil
}

func listenUDP() (trs map[string]*quic.Transport, err error) {
	trs = make(map[string]*quic.Transport)
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return
	}
	for _, a := range addrs {
		addr, ok := a.(*net.IPNet)
		if !ok || addr.IP.IsLoopback() {
			continue
		}
		udpAddr, e := net.ResolveUDPAddr("udp", addr.IP.String()+":0")
		if e != nil {
			return nil, e
		}
		conn, e := net.ListenUDP("udp", udpAddr)
		if e != nil {
			return nil, e
		}
		trs[conn.LocalAddr().String()] = &quic.Transport{Conn: conn}
		slog.Debug("Created QUIC transport:", "localAddr", conn.LocalAddr())
	}
	return
}

func (s *Service) Stop() error {
	slog.Warn("Stopping QUIC service")
	return nil
}

func (s *Service) handleClient(ctx context.Context, tr *quic.Transport, tlsConf *tls.Config, pid mpc.PID, addr net.Addr, conns chan<- net.Conn) (err error) {
	var c quic.Connection
	if c, err = tr.Dial(ctx, addr, tlsConf, s.qConf); err != nil {
		slog.Error(err.Error())
		return // give up to retry
	}
	slog.Info("Started QUIC client:", "peer", pid, "localAddr", c.LocalAddr(), "remoteAddr", c.RemoteAddr())

	err = util.Retry(ctx, func() (err error) {
		var st quic.Stream
		if st, err = c.OpenStreamSync(ctx); err != nil {
			if util.IsCanceledOrTimeout(err) {
				// give up to re-establish connection
				return util.Permanent(err)
			}
			// otherwise, retry
			slog.Error("Opening QUIC stream:", "peer", pid, "err", err)
			return
		}
		slog.Debug("Opened outgoing QUIC stream:", "peer", pid, "remoteAddr", c.RemoteAddr())

		// non-blocking until len(conns) == s.mpc.Threads
		conns <- &Conn{Connection: c, Stream: st}
		return
	})()
	return
}

// Conn implements net.Conn interface,
// wrapping the underlying quic.Connection and quic.Stream
type Conn struct {
	quic.Connection
	quic.Stream
}

func (s *Service) handleServer(ctx context.Context, tr *quic.Transport, tlsConf *tls.Config, pid mpc.PID, conns chan<- net.Conn) (err error) {
	l, err := tr.Listen(tlsConf, s.qConf)
	defer util.Cleanup(&err, l.Close)
	slog.Info("Started QUIC server:", "peer", pid, "localAddr", l.Addr())

	err = util.Retry(ctx, func() (err error) {
		var c quic.Connection
		if c, err = l.Accept(ctx); err != nil {
			if err == context.Canceled {
				return util.Permanent(err)
			}
			return // retry
		}
		defer util.Cleanup(&err, l.Close)
		slog.Debug("Accepted incoming QUIC connection:", "peer", pid, "remoteAddr", c.RemoteAddr())

		// this is done synchronously, because normally
		// we expect only one connection (!= stream) per peer
		return util.Retry(ctx, func() (err error) {
			return s.handleServerConn(ctx, pid, conns, c)
		})()
	})()
	return
}

func (s *Service) handleServerConn(ctx context.Context, pid mpc.PID, conns chan<- net.Conn, c quic.Connection) (err error) {
	var st quic.Stream
	if st, err = c.AcceptStream(ctx); err != nil {
		if util.IsCanceledOrTimeout(err) {
			// give up to re-establish connection
			return util.Permanent(err)
		}
		// otherwise, retry
		slog.Error("Accepting QUIC stream:", "err", err)
		return
	}
	slog.Debug("Accepted incoming QUIC stream:", "peer", pid, "remoteAddr", c.RemoteAddr())

	// non-blocking until len(conns) == s.mpc.Threads
	conns <- &Conn{Connection: c, Stream: st}
	return
}

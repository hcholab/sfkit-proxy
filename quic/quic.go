package quic

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"reflect"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/hcholab/sfkit-proxy/ice"
	"github.com/hcholab/sfkit-proxy/mpc"
	"github.com/hcholab/sfkit-proxy/util"
)

type tlsConfsGetter func(context.Context, mpc.PID, net.PacketConn) (<-chan *ice.TLSConf, io.Closer, error)

type Service struct {
	mpc         *mpc.Config
	qConf       *quic.Config
	errs        chan<- error
	getTLSConfs tlsConfsGetter
}

const retryMs = 1000

var errDone = errors.New("Done")

func NewService(mpcConf *mpc.Config, tcg tlsConfsGetter, errs chan<- error) (s *Service, err error) {
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

	udpConn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return
	}
	slog.Debug("Created UDP connection:", "localAddr", udpConn.LocalAddr(), "peerPID", peerPID)

	tr := &quic.Transport{Conn: udpConn}
	rpc := newRawPacketConn(ctx, tr)
	tcs, c, err := s.getTLSConfs(ctx, peerPID, rpc)
	if err != nil {
		return
	}

	conns := make(chan net.Conn, s.mpc.Threads)
	util.Go(ctx, s.errs, func() (err error) {
		defer util.Cleanup(&err, tr.Close)
		defer util.Cleanup(&err, c.Close)

		err = util.Retry(ctx, func() (err error) {
			select {
			case tc := <-tcs:
				slog.Debug("Using TLS config for", "peerPID", peerPID)

				if err = util.Retry(ctx, func() (err error) {
					if s.mpc.IsClient(peerPID) {
						err = s.handleClient(ctx, tr, tc.Config, peerPID, tc.RemoteAddr, conns)
					} else {
						err = s.handleServer(ctx, tr, tc.Config, peerPID, conns)
					}
					if util.IsTimeout(err) {
						slog.Warn("QUIC connection timeout, retrying:", "peerPID", peerPID)
						err = nil // retry the connection
					} else if err != nil {
						slog.Error("TLSConf error:", "peerPID", peerPID, "err", err.Error(), "isClient", s.mpc.IsClient(peerPID), "isPermanent", util.IsPermanent(err), "errType", reflect.TypeOf(err))
					}
					return
				})(); errors.Is(err, errDone) {
					slog.Error("QUIC connection done:", "peerPID", peerPID)
					err = nil // handle next TLSConf
				} else if err != nil {
					slog.Error("quic.GetConns():", "peerPID", peerPID, "err", err.Error())
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			return
		})()
		if err != nil {
			slog.Error("quic.GetConns():", "err", err)
		}
		return
	})
	return conns, nil
}

func (s *Service) Stop() error {
	slog.Warn("Stopping QUIC service")
	return nil
}

func (s *Service) handleClient(ctx context.Context, tr *quic.Transport, tlsConf *tls.Config, pid mpc.PID, addr net.Addr, conns chan<- net.Conn) (err error) {
	var c quic.Connection
	if c, err = tr.Dial(ctx, addr, tlsConf, s.qConf); err != nil {
		slog.Error("QUIC dial:", "addr", addr.String(), "err", err.Error())
		return // give up to retry the connection
	}
	slog.Info("Started QUIC client:", "peer", pid, "localAddr", c.LocalAddr(), "remoteAddr", c.RemoteAddr())

	nConns := 0
	err = util.Retry(ctx, func() (err error) {
		var st quic.Stream
		if st, err = c.OpenStreamSync(ctx); err != nil {
			if util.IsCanceledOrTimeout(err) {
				return util.Permanent(err) // give up, possibly retry the connection
			}
			// otherwise, retry stream opening
			slog.Error("Opening QUIC stream:", "peer", pid, "err", err)
			return
		}
		slog.Debug("Opened outgoing QUIC stream:", "peer", pid, "remoteAddr", c.RemoteAddr())

		conns <- &Conn{Connection: c, Stream: st}
		nConns++
		if nConns == s.mpc.Threads {
			return util.Permanent(errDone)
		}
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

func (c *Conn) Write(p []byte) (n int, err error) {
	n, err = c.Stream.Write(p)
	slog.Debug("Wrote to QUIC stream:", "addr", c.Connection.RemoteAddr(), "n", n, "err", err)
	return n, err
}

func (s *Service) handleServer(ctx context.Context, tr *quic.Transport, tlsConf *tls.Config, pid mpc.PID, conns chan<- net.Conn) (err error) {
	l, err := tr.Listen(tlsConf, s.qConf)
	if err != nil {
		slog.Error("QUIC listen:", "pid", pid, "err", err.Error())
		return
	}
	defer util.Cleanup(&err, func() error {
		slog.Warn("Closing QUIC listener:", "pid", pid, "localAddr", l.Addr())
		return l.Close()
	})
	slog.Info("Started QUIC server:", "peer", pid, "localAddr", l.Addr())

	err = util.Retry(ctx, func() (err error) {
		var c quic.Connection
		if c, err = l.Accept(ctx); err != nil {
			if err == context.Canceled {
				return util.Permanent(err)
			}
			return // retry
		}
		defer util.Cleanup(&err, func() error {
			slog.Warn("Closing QUIC connection:", "peer", pid, "localAddr", c.LocalAddr(), "remoteAddr", c.RemoteAddr())
			return l.Close()
		})
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

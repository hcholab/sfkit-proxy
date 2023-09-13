package quic

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/quic-go/quic-go"
	"golang.org/x/sync/errgroup"

	"github.com/hcholab/sfkit-proxy/conn"
	"github.com/hcholab/sfkit-proxy/mpc"
	"github.com/hcholab/sfkit-proxy/util"
)

type packetConnsGetter func(context.Context, mpc.PID) (<-chan *conn.PacketConn, io.Closer, error)

type Service struct {
	mpc            *mpc.Config
	qConf          *quic.Config
	errs           *errgroup.Group
	getPacketConns packetConnsGetter
}

const retryMs = 1000

func NewService(mpcConf *mpc.Config, pc packetConnsGetter, errs *errgroup.Group) (s *Service, err error) {
	qc := &quic.Config{
		KeepAlivePeriod: 15 * time.Second,
	}
	s = &Service{mpcConf, qc, errs, pc}
	slog.Debug("Started QUIC service")
	return
}

// GetConns establishes a QUIC connection stream with a peer,
// and returns a *conn.Conn channel, which allows the client
// to subscribe to changes in the connection.
func (s *Service) GetConns(ctx context.Context, peerPID mpc.PID) (_ <-chan *conn.Conn, err error) {
	slog.Debug("Getting connection for", "peerPID", peerPID)
	pcs, c, err := s.getPacketConns(ctx, peerPID)
	if err != nil {
		return
	}

	conns := make(chan *conn.Conn, s.mpc.Threads)
	s.errs.Go(func() (err error) {
		defer util.Cleanup(&err, c.Close)

		err = util.Retry(ctx, func() (err error) {
			select {
			case pc := <-pcs:
				slog.Debug("Obtained connection for", "peerPID", peerPID)
				defer util.Cleanup(&err, pc.Close)

				tr := &quic.Transport{Conn: pc}
				defer util.Cleanup(&err, tr.Close)

				var tlsConf *tls.Config
				if tlsConf, err = generateTLSConfig(pc.LocalAddr()); err != nil {
					return util.Permanent(err)
				}

				if err = util.Retry(ctx, func() error {
					if s.mpc.IsClient(peerPID) {
						return s.handleClient(ctx, tr, tlsConf, peerPID, pc.RemoteAddr(), conns)
					} else {
						return s.handleServer(ctx, tr, tlsConf, peerPID, conns)
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

func (s *Service) Stop() error {
	slog.Warn("Stopping QUIC service")
	return nil
}

func (s *Service) handleClient(ctx context.Context, tr *quic.Transport, tlsConf *tls.Config, pid mpc.PID, addr net.Addr, conns chan<- *conn.Conn) (err error) {
	var c quic.Connection
	if c, err = tr.Dial(ctx, addr, tlsConf, s.qConf); err != nil {
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
		conns <- &conn.Conn{Connection: c, Stream: st}
		return
	})()
	return
}

func (s *Service) handleServer(ctx context.Context, tr *quic.Transport, tlsConf *tls.Config, pid mpc.PID, conns chan<- *conn.Conn) (err error) {
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

func (s *Service) handleServerConn(ctx context.Context, pid mpc.PID, conns chan<- *conn.Conn, c quic.Connection) (err error) {
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
	conns <- &conn.Conn{Connection: c, Stream: st}
	return
}

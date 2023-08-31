package quic

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"log/slog"

	"github.com/quic-go/quic-go"

	"github.com/hcholab/sfkit-proxy/conn"
	"github.com/hcholab/sfkit-proxy/mpc"
	"github.com/hcholab/sfkit-proxy/util"
)

type packetConnGetter func(context.Context, mpc.PID) (<-chan *conn.PacketConn, error)

type Service struct {
	mpc     *mpc.Config
	tlsConf *tls.Config
	qConf   *quic.Config

	getPacketConns packetConnGetter
}

const retryMs = 1000

func NewService(mpcConf *mpc.Config, pc packetConnGetter) (s *Service, err error) {
	s = &Service{mpcConf, nil, &quic.Config{}, pc}
	if s.tlsConf, err = generateTLSConfig(); err != nil {
		return
	}
	slog.Debug("Started QUIC service")
	return
}

// GetConn establishes a QUIC connection stream with a peer,
// and returns a *conn.Conn channel, which allows the client
// to subscribe to changes in the connection.
func (s *Service) GetConn(ctx context.Context, peerPID mpc.PID) (_ <-chan *conn.Conn, err error) {
	slog.Debug(">>>>> quic.GetConn:", "peerPID", peerPID)
	pcs, err := s.getPacketConns(ctx, peerPID)
	if err != nil {
		return
	}
	slog.Debug(">>>> quick.GetConn:", "pcs", pcs)

	conns := make(chan *conn.Conn, s.mpc.Threads)
	go func() {
		for {
			select {
			case pc := <-pcs:
				slog.Debug(">>>>> quic.GetConn:", "pc", pc)
				var err error
				tr := &quic.Transport{Conn: pc}
				defer util.Cleanup(&err, tr.Close)

				if s.mpc.IsClient(peerPID) {
					err = s.listenClient(ctx, tr, peerPID, pc.RemoteAddr(), conns)
				} else {
					err = s.listenServer(ctx, tr, peerPID, conns)
				}
				if err != nil {
					slog.Error(err.Error())
					continue // TODO should we retry ?
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return conns, nil
}

func (s *Service) Stop() error {
	slog.Warn("Stopping QUIC service")
	return nil
}

func (s *Service) listenClient(
	ctx context.Context,
	tr *quic.Transport,
	pid mpc.PID,
	addr net.Addr,
	conns chan<- *conn.Conn,
) (err error) {
	c, err := tr.Dial(ctx, addr, s.tlsConf, s.qConf)
	if err != nil {
		return
	}
	slog.Info(
		"Started QUIC client:",
		"peer",
		pid,
		"localAddr",
		c.LocalAddr(),
		"remoteAddr",
		c.RemoteAddr(),
	)
	for {
		var st quic.Stream
		if st, err = c.OpenStream(); err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				err = nil
				return
			}
			// otherwise, retry after sleep; TODO exponential backoff ?
			slog.Error("Opening QUIC stream:", "peer", pid, "err", err)
			time.Sleep(retryMs * time.Millisecond)
			continue
		}
		defer util.Cleanup(&err, st.Close)
		slog.Debug("Opened outgoing QUIC stream:", "peer", pid, "remoteAddr", c.RemoteAddr())

		// non-blocking until len(conns) == s.mpc.Threads
		conns <- &conn.Conn{Connection: c, Stream: st}
	}
}

// RunServer listens on *quic.Transport and handles incoming connections and their streams
func (s *Service) listenServer(
	ctx context.Context,
	tr *quic.Transport,
	pid mpc.PID,
	conns chan<- *conn.Conn,
) (err error) {
	l, err := tr.Listen(s.tlsConf, s.qConf)
	defer util.Cleanup(&err, l.Close)
	slog.Info("Started QUIC listener:", "peer", pid, "localAddr", l.Addr())

	for {
		var c quic.Connection
		if c, err = l.Accept(ctx); err != nil {
			if err == context.Canceled {
				err = nil
				return
			}
			// retry after sleep; TODO exponential backoff ?
			time.Sleep(retryMs * time.Millisecond)
			continue
		}
		defer util.Cleanup(&err, l.Close)
		slog.Debug("Accepted incoming QUIC connection:", "peer", pid, "remoteAddr", c.RemoteAddr())

		// this is done synchronously, because normally
		// we expect only one connection (!= stream) per peer
		s.handleServerConn(ctx, pid, conns, c)
	}
}

func (s *Service) handleServerConn(
	ctx context.Context,
	pid mpc.PID,
	conns chan<- *conn.Conn,
	c quic.Connection,
) {
	for {
		var st quic.Stream
		var err error
		if st, err = c.AcceptStream(ctx); err != nil {
			// TODO handle more fully
			slog.Error("Accepting QUIC stream:", "err", err)
			if e, ok := err.(net.Error); ok && e.Timeout() {
				// connection is closed due to a timeout, simply exit
				return
			}
			// otherwise, retry after sleep
			time.Sleep(retryMs * time.Millisecond)
			continue
		}
		defer util.Cleanup(&err, st.Close)
		slog.Debug("Accepted incoming QUIC stream:", "peer", pid, "remoteAddr", c.RemoteAddr())

		// non-blocking until len(conns) == s.mpc.Threads
		conns <- &conn.Conn{Connection: c, Stream: st}
	}
}

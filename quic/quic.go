package quic

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/url"
	"time"

	"github.com/quic-go/quic-go"
	"golang.org/x/sync/errgroup"

	// TODO replace with native slog in Go 1.21
	"golang.org/x/exp/slog"
)

type Service struct {
	tr   *quic.Transport
	conn *Connection
}

const retryMs = 1000

func NewService(ctx context.Context, uri *url.URL, errs *errgroup.Group) (s *Service, err error) {
	s = &Service{}
	addr, err := net.ResolveUDPAddr(uri.Scheme, uri.Host)
	if err != nil {
		return
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return
	}
	slog.Debug("Opened UDP socket:", "localAddr", conn.LocalAddr().String())
	s.tr = &quic.Transport{Conn: conn}
	s.conn = &Connection{ctx: ctx, tr: s.tr}

	tlsConf, err := generateTLSConfig()
	if err != nil {
		return
	}
	errs.Go(func() error {
		return s.startServer(ctx, tlsConf)
	})
	return
}

func (s *Service) Connection() *Connection {
	return s.conn
}

func (s *Service) Stop() error {
	slog.Warn("Stopping QUIC service")
	return s.tr.Close()
}

// RunServer listens on *quic.Transport and handles incoming connections and their streams
func (s *Service) startServer(ctx context.Context, tlsConf *tls.Config) (err error) {
	server, err := s.tr.Listen(tlsConf, nil)
	defer func() {
		err = server.Close()
	}()
	slog.Info("Started QUIC server:", "addr", server.Addr().String())

	for {
		if conn, e := server.Accept(ctx); e != nil {
			// TODO handle fully
			if e == context.Canceled {
				return
			}
			slog.Error("Accepting QUIC connection:", "err", e)
		} else {
			slog.Debug("Accepted incoming QUIC connection")
			go handleServerConn(ctx, conn)
		}
	}
}

func handleServerConn(ctx context.Context, conn quic.Connection) {
	for {
		if s, err := conn.AcceptStream(ctx); err != nil {
			// TODO handle more fully
			slog.Error("Accepting QUIC stream:", "err", err)
			if e, ok := err.(net.Error); ok && e.Timeout() {
				// connection is closed due to a timeout, simply exit
				return
			}
			// otherwise, retry after sleep
			time.Sleep(retryMs * time.Millisecond)
		} else {
			slog.Debug("Accepted incoming QUIC stream")
			go handleServerStream(s)
		}
	}
}

func handleServerStream(s quic.Stream) {
	defer func() {
		if err := s.Close(); err != nil {
			slog.Error("Closing QUIC stream:", "err", err)
		} else {
			slog.Debug("Closed QUIC stream")
		}
	}()

	if b, err := io.ReadAll(s); err != nil {
		// TODO handle fully
		slog.Error("Reading QUIC stream:", "err", err)
	} else {
		// TODO replace with real implementation;
		// for now, this just echoes the message back to the client
		slog.Debug("Got", "client_message", string(b))
		if n, err := s.Write(b); err != nil {
			slog.Error("Writing to stream:", "err", err, "nbytes", n)
		}
	}
}

type Connection struct {
	ctx context.Context
	tr  *quic.Transport
}

func (c *Connection) Read(p []byte) (n int, err error) {
	if n, _, err = c.tr.ReadNonQUICPacket(c.ctx, p); err != nil {
		// TODO handle fully
		if err != context.Canceled {
			slog.Error("Receiving non-QUIC packet:", "err", err)
		}
	} else {
		slog.Debug("Received a non-QUIC packet:", "bytes", string(p)) // TODO remove
	}
	return
}

func (c *Connection) Write(p []byte, addr net.Addr) (int, error) {
	return c.tr.WriteTo(p, addr)
}

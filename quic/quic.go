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
	udpConn *net.UDPConn
	tlsConf *tls.Config
	tr      *quic.Transport
}

const retryMs = 1000

func NewService(ctx context.Context, uri *url.URL, errs *errgroup.Group) (s *Service, err error) {
	s = &Service{}
	addr, err := net.ResolveUDPAddr(uri.Scheme, uri.Host)
	if err != nil {
		return
	}
	if s.udpConn, err = net.ListenUDP("udp", addr); err != nil {
		return
	}
	slog.Info("Opened UDP socket:", "localAddr", s.udpConn.LocalAddr())
	s.tr = &quic.Transport{Conn: s.udpConn}
	s.tlsConf = generateTLSConfig()

	go s.readNonQUICPackets(ctx)

	errs.Go(func() error {
		return s.startServer(ctx)
	})
	err = errs.Wait()
	return
}

func (s *Service) Stop() (err error) {
	return s.udpConn.Close()
}

func (s *Service) readNonQUICPackets(ctx context.Context) {
	for {
		b := make([]byte, 1024)
		slog.Info("Waiting for a non-QUIC packet")
		if n, addr, err := s.tr.ReadNonQUICPacket(ctx, b); err != nil {
			// TODO handle fully
			slog.Error("Receiving non-QUIC packet:", "err", err)
		} else {
			// TODO handle properly
			slog.Info("Received non-QUIC packet:", "len", n, "addr", addr, "content", string(b))
		}
	}
}

// RunServer listens on *quic.Transport and handles incoming connections and their streams
func (s *Service) startServer(ctx context.Context) (err error) {
	server, err := s.tr.Listen(s.tlsConf, nil)
	defer func() {
		err = server.Close()
	}()
	slog.Info("Started QUIC server:", "addr", server.Addr())

	for {
		if conn, e := server.Accept(ctx); e != nil {
			// TODO handle fully
			slog.Error("Accepting QUIC connection:", "err", e)
		} else {
			slog.Info("Accepted incoming QUIC connection")
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
			slog.Info("Accepted incoming QUIC stream")
			go handleServerStream(s)
		}
	}
}

func handleServerStream(s quic.Stream) {
	defer func() {
		if err := s.Close(); err != nil {
			slog.Error("Closing QUIC stream:", "err", err)
		} else {
			slog.Info("Closed QUIC stream")
		}
	}()

	if b, err := io.ReadAll(s); err != nil {
		// TODO handle fully
		slog.Error("Reading QUIC stream:", "err", err)
	} else {
		// TODO replace with real implementation;
		// for now, this just echoes the message back to the client
		slog.Info("Got", "client_message", string(b))
		if n, err := s.Write(b); err != nil {
			slog.Error("Writing to stream:", "err", err, "nbytes", n)
		}
	}
}

// A wrapper for io.Writer that also logs the message.
type loggingWriter struct{ io.Writer }

func (w loggingWriter) Write(b []byte) (int, error) {
	slog.Info("Got", "client_message", string(b))
	return w.Writer.Write(b)
}

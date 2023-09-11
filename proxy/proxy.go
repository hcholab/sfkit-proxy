package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"sync"

	"github.com/armon/go-socks5"
	"golang.org/x/sync/errgroup"

	"github.com/hcholab/sfkit-proxy/conn"
	"github.com/hcholab/sfkit-proxy/mpc"
)

type remoteConnGetter func(context.Context, mpc.PID, *errgroup.Group) (<-chan *conn.Conn, error)

type Service struct {
	mpc *mpc.Config
	l   net.Listener

	remoteConns    map[mpc.PID]<-chan *conn.Conn
	getRemoteConns remoteConnGetter

	errs *errgroup.Group
}

func NewService(ctx context.Context, listenURI *url.URL, mpcConf *mpc.Config, rcg remoteConnGetter, errs *errgroup.Group) (s *Service, err error) {
	slog.Debug("Starting SOCKS service")

	s = &Service{
		mpc:            mpcConf,
		remoteConns:    make(map[mpc.PID]<-chan *conn.Conn),
		getRemoteConns: rcg,
		errs:           errs,
	}
	slog.Debug("MPC:", "config", mpcConf)

	if len(mpcConf.ServerPIDs) > 0 {
		// set up SOCKS5 listener that accepts
		// connections from local proxy clients
		if err = s.createSocksListener(listenURI); err != nil {
			return
		}
	}

	// create a connection channel for each remote (server or client) peer
	if err = s.initRemoteConns(ctx, errs); err != nil {
		return
	}

	// set up local TCP server endpoints to forward
	// connections from remote clients to
	if err = s.initLocalConns(ctx, errs); err != nil {
		return
	}

	slog.Debug("Started proxy service")
	return
}

func (s *Service) Stop() (err error) {
	slog.Warn("Stopping proxy service")
	return s.l.Close()
}

func (s *Service) createSocksListener(listenURI *url.URL) (err error) {
	server, err := socks5.New(&socks5.Config{
		Dial: s.dialRemote,
	})
	if err != nil {
		return
	}
	if s.l, err = net.Listen(listenURI.Scheme, listenURI.Host); err != nil {
		return
	}
	s.errs.Go(func() (err error) {
		if err = server.Serve(s.l); err == nil {
			// TODO implement reconnect ?
			return
		}
		if opErr, ok := err.(*net.OpError); ok &&
			opErr.Err.Error() == "use of closed network connection" {
			err = nil
			return
		}
		return
	})
	slog.Debug("SOCKS proxy is listening on:", "addr", toURL(s.l.Addr()))
	return
}

func toURL(addr net.Addr) *url.URL {
	return &url.URL{
		Scheme: addr.Network(),
		Host:   addr.String(),
	}
}

func (s *Service) dialRemote(ctx context.Context, network, addr string) (conn net.Conn, err error) {
	slog.Debug("SOCKS dial:", "network", network, "addr", addr)
	addrPort, err := netip.ParseAddrPort(addr)
	if err != nil {
		return
	}
	serverPID, ok := s.mpc.ServerPIDs[addrPort]
	if !ok {
		err = fmt.Errorf("no PID found for address: %s", addr)
		return
	}
	conn = <-s.remoteConns[serverPID]
	slog.Debug("SOCKS dial:", "PID", serverPID)

	// Send remote port in big-endian format to the remote peer
	bPort := []byte{byte(addrPort.Port() >> 8), byte(addrPort.Port())}
	_, err = conn.Write(bPort)
	return
}

type pidConn struct {
	Conn <-chan *conn.Conn
	mpc.PID
}

func (s *Service) initRemoteConns(ctx context.Context, errs *errgroup.Group) (err error) {
	const logName = "remote peer connections"
	nConns := len(s.mpc.PeerPIDs)
	if nConns == 0 {
		slog.Warn("No " + logName + " to initiate")
		return
	}

	pidConns := make(chan *pidConn, nConns)

	for _, pid := range s.mpc.PeerPIDs {
		remotePID := pid

		s.errs.Go(func() (err error) {
			c, err := s.getRemoteConns(ctx, remotePID, s.errs)
			if err == nil {
				pidConns <- &pidConn{c, remotePID}
			}
			return
		})
	}
	slog.Debug("Initiating "+logName, "peers", s.mpc.PeerPIDs)

	for i := 0; i < nConns; i++ {
		pc := <-pidConns
		s.remoteConns[pc.PID] = pc.Conn
	}
	slog.Debug("Initiated " + logName)
	return
}

func (s *Service) initLocalConns(ctx context.Context, errs *errgroup.Group) (err error) {
	const logSuffix = "local listeners for remote clients:"
	slog.Debug("Initiating " + logSuffix)

	clientPIDs := make([]mpc.PID, 0, len(s.mpc.PIDClients))
	wg := &sync.WaitGroup{}

	for clientPID, localAddrs := range s.mpc.PIDClients {
		go s.initLocalConn(ctx, localAddrs, s.remoteConns[clientPID])

		clientPIDs = append(clientPIDs, clientPID)
		wg.Add(1)
	}
	wg.Wait()

	slog.Debug("Initiated "+logSuffix, "peers", clientPIDs)
	return
}

func (s *Service) initLocalConn(ctx context.Context, localAddrs []netip.AddrPort, remoteConns <-chan *conn.Conn) {
	tcpConns := make(chan *net.TCPConn, len(localAddrs))

	for _, addr := range localAddrs {
		localAddr := addr

		s.errs.Go(func() (err error) {
			// TODO implement re-connect ?
			c, err := net.Dial("tcp", localAddr.String())
			if err != nil {
				return
			}
			if tcpConn, ok := c.(*net.TCPConn); ok {
				tcpConns <- tcpConn
			} else {
				err = fmt.Errorf("not a TCP connection: %s", c)
			}
			return
		})
	}

	localConns := make([]*net.TCPConn, 0, len(localAddrs))
	for i := 0; i < len(localAddrs); i++ {
		localConns = append(localConns, <-tcpConns)
	}

	s.errs.Go(func() (err error) {
		return s.handleClientConns(ctx, localConns, remoteConns)
	})
}

const tcpBufSize = 4096 // TODO measure performance and adjust as needed

func (s *Service) handleClientConns(ctx context.Context, localConns []*net.TCPConn, remoteConns <-chan *conn.Conn) (err error) {
	buf := make([]byte, tcpBufSize)
	for {
		select {
		case rc := <-remoteConns:
			// retrieve local connection based on destination port sent by the client
			var lc *net.TCPConn
			if lc, err = getLocalConn(localConns, rc); err != nil {
				return
			}

			// proxy remote request-response through the connection
			if _, err = readWriteStream(buf, rc, lc); err != nil {
				if err == io.EOF {
					err = nil
					return // TODO: implement reconnect
				} else if ctx.Err() == context.Canceled {
					err = nil
					return
				}
				slog.Error(err.Error())
				// retry
			}
		case <-ctx.Done():
			return
		}
	}
}

func getLocalConn(localConns []*net.TCPConn, rc *conn.Conn) (lc *net.TCPConn, err error) {
	// read destination port in big-endian format from the remote peer
	bPort := make([]byte, 2)
	if _, err = io.ReadFull(rc, bPort); err != nil {
		return
	}
	port := int(bPort[0])<<8 | int(bPort[1])

	for _, c := range localConns {
		if c.RemoteAddr().(*net.TCPAddr).Port == int(port) {
			lc = c
			return
		}
	}
	err = fmt.Errorf("no local connection found for port %d", port)
	return
}

func readWriteStream(buf []byte, remoteConn *conn.Conn, localConn *net.TCPConn) (n int, err error) {
	// read a request from the remote client
	if n, err = remoteConn.Read(buf); err != nil {
		return
	}
	// forward it to the local server
	if n, err = localConn.Write(buf[:n]); err != nil {
		return
	}
	// read a response from the local server
	if n, err = localConn.Read(buf); err != nil {
		return
	}
	// forward the response back to the remote client
	n, err = remoteConn.Write(buf[:n])
	return
}

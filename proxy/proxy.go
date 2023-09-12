package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"

	"github.com/armon/go-socks5"
	"golang.org/x/sync/errgroup"

	"github.com/hcholab/sfkit-proxy/conn"
	"github.com/hcholab/sfkit-proxy/mpc"
	"github.com/hcholab/sfkit-proxy/util"
)

type remoteConnsGetter func(context.Context, mpc.PID, *errgroup.Group) (<-chan *conn.Conn, error)

type Service struct {
	mpc *mpc.Config
	l   net.Listener

	remoteConns    map[mpc.PID]<-chan *conn.Conn
	getRemoteConns remoteConnsGetter

	errs *errgroup.Group
}

func NewService(ctx context.Context, listenURI *url.URL, mpcConf *mpc.Config, rcg remoteConnsGetter, errs *errgroup.Group) (s *Service, err error) {
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
	s.initLocalConns(ctx, errs)

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
	Conns <-chan *conn.Conn
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
			conns, err := s.getRemoteConns(ctx, remotePID, s.errs)
			if err == nil {
				pidConns <- &pidConn{conns, remotePID}
			}
			return
		})
	}
	slog.Debug("Initiating "+logName, "peers", s.mpc.PeerPIDs)

	for i := 0; i < nConns; i++ {
		pc := <-pidConns
		s.remoteConns[pc.PID] = pc.Conns
	}
	slog.Debug("Initiated " + logName)
	return
}

func (s *Service) initLocalConns(ctx context.Context, errs *errgroup.Group) {
	const logSuffix = "local listeners for remote clients:"
	slog.Debug("Initiating " + logSuffix)

	clientPIDs := make([]mpc.PID, 0, len(s.mpc.PIDClients))

	for clientPID, addrs := range s.mpc.PIDClients {
		remoteConns := s.remoteConns[clientPID]
		localAddrs := addrs
		go s.handleClientConns(ctx, localAddrs, remoteConns)
		clientPIDs = append(clientPIDs, clientPID)
	}

	slog.Debug("Initiated "+logSuffix, "peers", clientPIDs)
}

func (s *Service) handleClientConns(ctx context.Context, localAddrs []netip.AddrPort, remoteConns <-chan *conn.Conn) (err error) {
	for {
		select {
		case remoteConn := <-remoteConns:
			s.errs.Go(func() error {
				return proxyRemoteClient(ctx, remoteConn, localAddrs)
			})
		case <-ctx.Done():
			return
		}
	}
}

const tcpBufSize = 4096 // TODO measure performance and adjust as needed

func proxyRemoteClient(ctx context.Context, remoteConn *conn.Conn, localAddrs []netip.AddrPort) (err error) {
	n := 0
	buf := make([]byte, tcpBufSize)

	for {
		var localConn *net.TCPConn
		if localConn, err = getLocalConn(localAddrs, remoteConn); err != nil {
			return
		}
		defer util.Cleanup(&err, localConn.Close)

		for {
			// read a request from the remote client
			if n, err = remoteConn.Read(buf); err != nil {
				if err == io.EOF || ctx.Err() == context.Canceled {
					err = nil
				}
				return
			}

			// forward it to the local server
			if _, err = localConn.Write(buf[:n]); err != nil {
				if err == io.EOF {
					err = nil
					// re-establish local TCP connection
					break // TODO: exponential backoff
				} else if ctx.Err() == context.Canceled {
					err = nil
				}
				return
			}

			// read a response from the local server
			if n, err = localConn.Read(buf); err != nil {
				if err == io.EOF {
					err = nil
					// re-establish local TCP connection
					break // TODO: exponential backoff
				} else if ctx.Err() == context.Canceled {
					err = nil
				}
				return
			}

			// forward the response back to the remote client
			if _, err = remoteConn.Write(buf[:n]); err != nil {
				if err == io.EOF || ctx.Err() == context.Canceled {
					err = nil
				}
				return
			}
		}
	}
}

func getLocalConn(localAddrs []netip.AddrPort, rc *conn.Conn) (lc *net.TCPConn, err error) {
	// read destination port in big-endian format from the remote peer
	bPort := make([]byte, 2)
	if _, err = io.ReadFull(rc, bPort); err != nil {
		return
	}
	port := uint16(bPort[0])<<8 | uint16(bPort[1])

	// look up local address:port based on the destination port
	var localAddr *netip.AddrPort
	for _, la := range localAddrs {
		if la.Port() == port {
			localAddr = &la
			break
		}
	}
	if localAddr == nil {
		err = fmt.Errorf("no local address corresponds to the port sent by the remote client: %d", port)
		return
	}

	// establish a new TCP connection to the local address:port
	var c net.Conn
	if c, err = net.Dial("tcp", localAddr.String()); err != nil {
		return
	}
	lc = c.(*net.TCPConn)
	return
}

package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"reflect"

	"github.com/armon/go-socks5"

	"github.com/hcholab/sfkit-proxy/mpc"
	"github.com/hcholab/sfkit-proxy/util"
)

type remoteConnsGetter func(context.Context, mpc.PID) (<-chan net.Conn, error)

type Service struct {
	mpc *mpc.Config
	l   net.Listener

	remoteConns    map[mpc.PID]<-chan net.Conn
	getRemoteConns remoteConnsGetter

	errs chan<- error
}

func NewService(ctx context.Context, wsReady <-chan any, listenURI *url.URL, mpcConf *mpc.Config, rcg remoteConnsGetter, errs chan<- error) (s *Service, err error) {
	slog.Debug("Starting SOCKS service")

	s = &Service{
		mpc:            mpcConf,
		remoteConns:    make(map[mpc.PID]<-chan net.Conn),
		getRemoteConns: rcg,
		errs:           errs,
	}
	slog.Debug("MPC:", "config", mpcConf)

	// channel to signal when SOCKS dialer can start proxying
	dialReady := make(chan any)

	if len(mpcConf.ServerPIDs) > 0 {
		// set up SOCKS5 listener that accepts
		// connections from local proxy clients
		if err = s.createSocksListener(ctx, dialReady, listenURI); err != nil {
			return
		}
	}

	<-wsReady

	// create a connection channel for each remote (server or client) peer
	if err = s.initRemoteConns(ctx); err != nil {
		return
	}

	// set up local TCP server endpoints to forward
	// connections from remote clients to
	s.initLocalConns(ctx)

	// signal dial readiness
	close(dialReady)

	slog.Debug("Started proxy service")
	return
}

func (s *Service) Stop() (err error) {
	slog.Warn("Stopping proxy service")
	if s.l != nil {
		err = s.l.Close()
	}
	return
}

func (s *Service) createSocksListener(ctx context.Context, dialReady <-chan any, listenURI *url.URL) (err error) {
	server, err := socks5.New(&socks5.Config{
		Dial: s.dialRemote(dialReady),
	})
	if err != nil {
		return
	}
	lc := net.ListenConfig{}
	if s.l, err = lc.Listen(ctx, listenURI.Scheme, listenURI.Host); err != nil {
		return
	}
	util.Go(ctx, s.errs, util.Retry(ctx, func() error {
		return server.Serve(s.l)
	}))
	slog.Debug("SOCKS proxy is listening on:", "addr", toURL(s.l.Addr()))
	return
}

func toURL(addr net.Addr) *url.URL {
	return &url.URL{
		Scheme: addr.Network(),
		Host:   addr.String(),
	}
}

type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func (s *Service) dialRemote(ready <-chan any) DialFunc {
	return func(_ context.Context, network, addr string) (conn net.Conn, err error) {
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

		slog.Debug("SOCKS dial: waiting for remote connections")
		<-ready

		conns, ok := s.remoteConns[serverPID]
		if !ok {
			err = fmt.Errorf("cannot find remote connectionection channel for PID %d", serverPID)
			return
		}
		conn = <-conns
		slog.Debug("SOCKS dial: sending server", "PID", serverPID, "port", addrPort.Port())

		// Send remote port in big-endian format to the remote peer
		bPort := []byte{byte(addrPort.Port() >> 8), byte(addrPort.Port())}
		_, err = conn.Write(bPort)
		return
	}
}

type pidConn struct {
	Conns <-chan net.Conn
	mpc.PID
}

func (s *Service) initRemoteConns(ctx context.Context) (err error) {
	const logName = "remote peer connections"
	nConns := len(s.mpc.PeerPIDs)
	if nConns == 0 {
		slog.Warn("No " + logName + " to initiate")
		return
	}

	pidConns := make(chan *pidConn, nConns)

	for _, pid := range s.mpc.PeerPIDs {
		remotePID := pid

		util.Go(ctx, s.errs, func() (err error) {
			conns, err := s.getRemoteConns(ctx, remotePID)
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

func (s *Service) initLocalConns(ctx context.Context) {
	const logSuffix = "local listeners for remote clients"
	nClients := len(s.mpc.PIDClients)
	if nClients == 0 {
		slog.Warn("No " + logSuffix + " to initiate")
		return
	}
	clientPIDs := make([]mpc.PID, 0, nClients)
	slog.Debug("Initiating " + logSuffix)

	for clientPID, addrs := range s.mpc.PIDClients {
		remoteConns := s.remoteConns[clientPID]
		localAddrs := addrs
		go s.handleClientConns(ctx, localAddrs, remoteConns)
		clientPIDs = append(clientPIDs, clientPID)
	}

	slog.Debug("Initiated "+logSuffix, "peers", clientPIDs)
}

func (s *Service) handleClientConns(ctx context.Context, localAddrs []netip.AddrPort, remoteConns <-chan net.Conn) (err error) {
	for {
		select {
		case remoteConn := <-remoteConns:
			util.Go(ctx, s.errs, func() error {
				return s.proxyRemoteClient(ctx, remoteConn, localAddrs)
			})
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) proxyRemoteClient(ctx context.Context, remoteConn net.Conn, localAddrs []netip.AddrPort) (err error) {
	localEOF := errors.New("LocalEOF")
	defer util.Cleanup(&err, func() error {
		slog.Warn("Proxy: closing remote client connection:", "remoteAddr", remoteConn.RemoteAddr())
		return remoteConn.Close()
	})

	err = util.Retry(ctx, func() (err error) {
		var localConn *net.TCPConn
		if localConn, err = getLocalConn(localAddrs, remoteConn); err != nil {
			return
		}
		defer util.Cleanup(&err, func() error {
			slog.Warn("Proxy: closing local server connection:", "localAddr", localConn.RemoteAddr())
			return localConn.Close()
		})

		errs := make(chan error, 1)

		cctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// proxy requests: local server <- remote client
		util.Go(cctx, errs, util.Retry(cctx, func() (err error) {
			slog.Debug("Copying local <- remote:", "localAddr", localConn.RemoteAddr(), "remoteAddr", remoteConn.RemoteAddr())
			nbytes, err := io.Copy(localConn, remoteConn)
			slog.Debug("Copied local <- remote:", "nbytes", nbytes, "localAddr", localConn.RemoteAddr(), "remoteAddr", remoteConn.RemoteAddr())
			if err == nil {
				// err == nil means remote connection was closed,
				// which is not expected so we give up
				slog.Error("Unexpected EOF from remote client:", "localAddr", localConn.RemoteAddr(), "remoteAddr", remoteConn.RemoteAddr())
				return util.Permanent(io.EOF)
			} else if e, ok := err.(*net.OpError); ok && e.Op == "write" {
				// writing to local connection failed,
				// so we retry after recreating it
				slog.Error("Unexpected error writing to local server:", "localAddr", localConn.RemoteAddr(), "remoteAddr", remoteConn.RemoteAddr(), "err", e)
				return util.Permanent(localEOF)
			} else {
				// give up
				slog.Error("Unexpected error copying local <- remote:", "err", err, "errType", reflect.TypeOf(err))
				err = util.Permanent(err)
			}
			return
		}))

		// proxy responses: local server -> remote client
		util.Go(cctx, errs, util.Retry(cctx, func() (err error) {
			slog.Debug("Copying local -> remote:", "localAddr", localConn.RemoteAddr(), "localAddrICE", localConn.LocalAddr(), "remoteAddr", remoteConn.RemoteAddr())
			nbytes, err := io.Copy(remoteConn, localConn)
			slog.Debug("Copied local -> remote:", "nbytes", nbytes, "localAddr", localConn.RemoteAddr(), "localAddrICE", localConn.LocalAddr(), "remoteAddr", remoteConn.RemoteAddr())
			if err == nil {
				// err == nil means local connection was closed,
				// so we exit and retry after recreating it
				slog.Error("Unexpected EOF from local server:", "localAddr", localConn.RemoteAddr(), "localAddrICE", localConn.LocalAddr(), "remoteAddr", remoteConn.RemoteAddr())
				return util.Permanent(localEOF)
			} else if e, ok := err.(*net.OpError); ok && e.Op == "read" {
				// another error reading from local conneciton,
				// so we retry after recreating it
				slog.Error("Unexpected error reading from local server:", "localAddr", localConn.RemoteAddr(), "localAddrICE", localConn.LocalAddr(), "remoteAddr", remoteConn.RemoteAddr(), "err", e)
				return util.Permanent(localEOF)
			} else {
				// give up
				slog.Error("Unexpected error copying local -> remote:", "localAddr", localConn.RemoteAddr(), "localAddrICE", localConn.LocalAddr(), "remoteAddr", remoteConn.RemoteAddr(), "err", err, "errType", reflect.TypeOf(err))
				err = util.Permanent(err)
			}
			return
		}))

		err = <-errs
		if errors.Is(err, localEOF) {
			err = nil
			slog.Warn("Retrying local connection:", "localAddr", localConn.RemoteAddr(), "localAddrICE", localConn.LocalAddr(), "remoteAddr", remoteConn.RemoteAddr())
			return // retry local connection
		}
		return util.Permanent(err)
	})()
	return
}

func getLocalConn(localAddrs []netip.AddrPort, rc net.Conn) (lc *net.TCPConn, err error) {
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
	slog.Debug("Dialing local listener:", "addr", localAddr.String())
	if c, err = net.Dial("tcp", localAddr.String()); err != nil {
		return
	}
	lc = c.(*net.TCPConn)
	slog.Debug("Dialed local listener:", "localAddr", lc.LocalAddr(), "remoteAddr", lc.RemoteAddr())
	return
}

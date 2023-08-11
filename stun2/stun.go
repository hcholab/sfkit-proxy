package stun

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/hcholab/sfkit-proxy/quic"
	"github.com/pion/stun"

	// TODO replace with builtins in Go 1.21
	"golang.org/x/exp/slices"
	"golang.org/x/exp/slog"
	"golang.org/x/sync/errgroup"
)

var (
	defaultServers = []string{
		// TODO can we rely on Google?
		"stun:stun.l.google.com:19302",
		"stun:stun1.l.google.com:19302",
		"stun:stun2.l.google.com:19302",
		"stun:stun3.l.google.com:19302",
		"stun:stun4.l.google.com:19302",

		// from Syncthing, should be reliable
		"stun:stun.syncthing.net:3478",
		"stun:stun.callwithus.com:3478",
		"stun:stun.counterpath.com:3478",
		"stun:stun.counterpath.net:3478",
		"stun:stun.ekiga.net:3478",
		"stun:stun.ideasip.com:3478",
		"stun:stun.internetcalls.com:3478",
		"stun:stun.schlund.de:3478",
		"stun:stun.sipgate.net:10000",
		"stun:stun.sipgate.net:3478",
		"stun:stun.voip.aebc.com:3478",
		"stun:stun.voiparound.com:3478",
		"stun:stun.voipbuster.com:3478",
		"stun:stun.voipstunt.com:3478",
		"stun:stun.xten.com:3478",
	}
)

type URI stun.URI

func DefaultServers() []string {
	return slices.Clone(defaultServers)
}

func ParseServers(servers []string) (uris []URI, err error) {
	for _, s := range servers {
		var u *stun.URI
		u, err = stun.ParseURI(s)
		if err != nil {
			return
		}
		uris = append(uris, URI(*u))
	}
	if len(uris) == 0 {
		err = fmt.Errorf("List of STUN servers must be non-empty")
	}
	return
}

type Service struct {
	client *stun.Client
	uris   []URI
}

func NewService(ctx context.Context, uris []URI, errs *errgroup.Group, qconn *quic.Connection) (s *Service, err error) {
	s = &Service{uris: uris}

	// TODO implement address hopping
	hostPort := net.JoinHostPort(uris[0].Host, strconv.Itoa(uris[0].Port))
	addr, err := net.ResolveUDPAddr("udp", hostPort)
	if err != nil {
		return
	}
	if s.client, err = stun.NewClient(&Connection{qconn, addr}); err != nil {
		return
	}

	// send async binding request to the STUN server
	msg := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	err = s.client.Start(msg, stunHandler)
	return
}

func stunHandler(res stun.Event) {
	var (
		addr stun.XORMappedAddress
		err  error
	)
	if res.Error != nil {
		err = res.Error
	} else {
		// decode XOR-MAPPED-ADDRESS attribute from message.
		slog.Debug("Response from STUN:", "message", res.Message)
		err = addr.GetFrom(res.Message)
	}
	if err != nil {
		slog.Error("Response from STUN:", "err", err)
		panic(err) //nolint
	}
	slog.Info("Obtained external address from STUN:", "addr", addr)
}

func (s *Service) Stop() error {
	slog.Warn("Stopping STUN service")
	return s.client.Close()
}

// Connection is a wrapper around quic.Connection
// used to send/receive STUN packets
type Connection struct {
	qconn *quic.Connection
	addr  net.Addr
}

func (c *Connection) Read(p []byte) (n int, err error) {
	if c.qconn == nil {
		err = checkLogError(fmt.Errorf("no underlying QUIC connection"))
		return
	}
	if n, err = c.qconn.Read(p); err == nil && !stun.IsMessage(p) {
		err = checkLogError(fmt.Errorf("not a STUN packet: %s", p))
	}
	if err != nil {
		return
	}
	slog.Debug("Received a STUN packet:", "bytes", string(p))
	return
}

func (c *Connection) Write(p []byte) (n int, err error) {
	if c.qconn == nil {
		err = checkLogError(fmt.Errorf("no underlying QUIC connection"))
		return
	}
	if !stun.IsMessage(p) {
		err = checkLogError(fmt.Errorf("not a STUN packet: %s", p))
		return
	}
	slog.Debug("Sending a STUN packet:", "bytes", string(p), "to", c.addr)
	return c.qconn.Write(p, c.addr)
}

func (c *Connection) Close() error {
	// we don't want to close the underlying QUIC connection
	// when STUN closes, so this is a dummy operation
	slog.Warn("Closing STUN connection")
	c.qconn = nil
	c.addr = nil
	return nil
}

func checkLogError(err error) error {
	if err != context.Canceled {
		slog.Error(err.Error())
	}
	return err
}

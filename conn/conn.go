package conn

import (
	"crypto/tls"
	"fmt"
	"net"

	"github.com/pion/ice/v2"
	"github.com/quic-go/quic-go"
)

// PacketConn implements net.PacketConn interface, wrapping the underlying *ice.Conn
type PacketConn struct {
	*ice.Conn
	TLSConf *tls.Config
}

func (c PacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	n, err = c.Read(p)
	if err != nil {
		return
	}
	addr = c.RemoteAddr()
	return
}

func (c PacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	rAddr := c.RemoteAddr()
	if addr == nil || addr.Network() != rAddr.Network() || addr.String() != rAddr.String() {
		err = fmt.Errorf("unexpected addr, must be %s://%s", rAddr.Network(), rAddr.String())
		return
	}
	return c.Write(p)
}

// Conn implements net.Conn interface,
// wrapping the underlying quic.Connection and quic.Stream
type Conn struct {
	quic.Connection
	quic.Stream
}

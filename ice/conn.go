package ice

import (
	"context"
	"math"
	"net"
	"os"
	"time"

	"github.com/hcholab/sfkit-proxy/quic"
)

// PacketConn implements net.PacketConn interface,
// wrapping the underlying QUIC connection
// for multiplexing with STUN traffic

type PacketConn struct {
	// read-only
	ctx  context.Context
	conn *quic.Connection

	// thread-safe
	closeCh    chan any
	readTimer  *time.Timer
	writeTimer *time.Timer
}

func newPacketConn(ctx context.Context, qconn *quic.Connection) *PacketConn {
	return &PacketConn{
		ctx:        ctx,
		conn:       qconn,
		closeCh:    make(chan any),
		readTimer:  newTimer(),
		writeTimer: newTimer(),
	}
}

func (c *PacketConn) LocalAddr() net.Addr {
	if c.isClosed() {
		return nil
	}
	return c.conn.GetListenAddress()
}

func (c *PacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	type Result struct {
		n    int
		addr net.Addr
	}
	r, err := readWrite(c.ctx, c, *c.writeTimer, func() (Result, error) {
		n, addr, err := c.conn.Read(p)
		return Result{n, addr}, err
	})
	return r.n, r.addr, err
}

func (c *PacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return readWrite(c.ctx, c, *c.writeTimer, func() (int, error) {
		return c.conn.Write(p, addr)
	})
}

func readWrite[R any](ctx context.Context, c *PacketConn, t time.Timer, op func() (R, error)) (result R, err error) {
	if c.isClosed() {
		err = net.ErrClosed
		return
	}

	rc := make(chan R, 1)
	errc := make(chan error, 1)

	go func() {
		r, err := op()
		if err != nil {
			errc <- err
			return
		}
		rc <- r
	}()

	select {
	case err = <-errc:
	case result = <-rc:
	case <-c.closeCh:
		err = net.ErrClosed
	case <-t.C:
		err = os.ErrDeadlineExceeded
	case <-ctx.Done():
		err = context.Canceled
	}
	return
}

func (c *PacketConn) SetDeadline(t time.Time) error {
	if c.isClosed() {
		return net.ErrClosed
	}
	c.SetWriteDeadline(t)
	c.SetReadDeadline(t)
	return nil
}

func (c *PacketConn) SetReadDeadline(t time.Time) error {
	if c.isClosed() {
		return net.ErrClosed
	}
	setUntil(c.readTimer, t)
	return nil
}

func (c *PacketConn) SetWriteDeadline(t time.Time) error {
	if c.isClosed() {
		return net.ErrClosed
	}
	setUntil(c.writeTimer, t)
	return nil
}

func newTimer() (t *time.Timer) {
	t = time.NewTimer(0)
	setUntil(t, time.Time{})
	return
}

func setUntil(tr *time.Timer, t time.Time) {
	var d time.Duration
	if t.IsZero() {
		d = time.Duration(math.MaxInt64)
	} else {
		d = time.Until(t)
	}
	tr.Reset(d)
}

func (c *PacketConn) isClosed() bool {
	select {
	case <-c.closeCh:
		return true
	default:
		return false
	}
}

func (c *PacketConn) Close() error {
	if c.isClosed() {
		return net.ErrClosed
	}
	// we don't want to close the underlying QUIC connection,
	// so just close PacketConn itself and exit
	close(c.closeCh)
	return nil
}

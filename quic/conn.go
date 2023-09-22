package quic

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

type contextDeadline struct {
	ctx    context.Context
	cancel context.CancelFunc
	t      chan time.Time
}

func newContextDeadline() contextDeadline {
	ctx, cancel := context.WithCancel(context.Background())
	d := contextDeadline{
		ctx:    ctx,
		cancel: cancel,
		t:      make(chan time.Time),
	}
	go d.monitor()
	return d
}

func (d *contextDeadline) monitor() {
	for t := range d.t {
		d.cancel() // Cancel the ongoing read if there's any

		if t.IsZero() {
			d.ctx, d.cancel = context.WithCancel(d.ctx)
		} else {
			d.ctx, d.cancel = context.WithDeadline(d.ctx, t)
		}
	}
}

// RawPacketConn implements net.RawPacketConn interface, which wraps the underlying *quic.Transport
// to send raw packets via multiplexing on the UDP port
type RawPacketConn struct {
	ctx context.Context
	tr  *quic.Transport

	readDeadline  contextDeadline
	writeDeadline contextDeadline
}

func newRawPacketConn(ctx context.Context, tr *quic.Transport) *RawPacketConn {
	return &RawPacketConn{ctx, tr, newContextDeadline(), newContextDeadline()}
}

func (c *RawPacketConn) GetTransport() *quic.Transport {
	return c.tr
}

func (c *RawPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	return c.handleOperation(c.readDeadline.ctx, c.readDeadline.cancel,
		func(ctx context.Context) (int, net.Addr, error) {
			return c.tr.ReadNonQUICPacket(ctx, p)
		},
	)
}

func (c *RawPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	n, _, err := c.handleOperation(c.writeDeadline.ctx, c.writeDeadline.cancel,
		func(_ context.Context) (n int, _ net.Addr, err error) {
			n, err = c.tr.WriteTo(p, addr)
			return
		},
	)
	return n, err
}

func (c *RawPacketConn) handleOperation(
	ctx context.Context,
	cancel context.CancelFunc,
	op func(ctx context.Context) (int, net.Addr, error),
) (
	int, net.Addr, error,
) {
	type res struct {
		addr net.Addr
		err  error
		n    int
	}
	for {
		ch := make(chan res)

		go func() {
			n, addr, err := op(ctx)
			ch <- res{addr, err, n}
		}()

		select {
		case <-ctx.Done():
			if ctx.Err() == context.Canceled {
				continue // deadline was changed, retry
			}
			return 0, nil, ctx.Err()
		case <-c.ctx.Done():
			return 0, nil, c.ctx.Err()
		case r := <-ch:
			return r.n, r.addr, r.err
		}
	}
}

func (c *RawPacketConn) Close() error {
	return errors.ErrUnsupported
}

func (c *RawPacketConn) LocalAddr() net.Addr {
	return c.tr.Conn.LocalAddr()
}

func (c *RawPacketConn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}

func (c *RawPacketConn) SetReadDeadline(t time.Time) error {
	c.readDeadline.t <- t
	return nil
}

func (c *RawPacketConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline.t <- t
	return nil
}

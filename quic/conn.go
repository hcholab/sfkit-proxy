package quic

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

type contextDeadline struct {
	ctx    context.Context
	cancel context.CancelFunc
	t      chan time.Time
}

func newContextDeadline(id string) contextDeadline {
	ctx, cancel := context.WithCancel(context.Background())
	d := contextDeadline{
		ctx:    ctx,
		cancel: cancel,
		t:      make(chan time.Time),
	}
	go d.monitor(id)
	return d
}

func (d *contextDeadline) monitor(id string) {
	for t := range d.t {
		d.cancel() // Cancel the ongoing read if there's any

		if t.IsZero() {

			d.ctx, d.cancel = context.WithCancel(d.ctx)
			slog.Warn("Deadline cleared:", "id", id, "ctx", d.ctx)
		} else {
			d.ctx, d.cancel = context.WithDeadline(d.ctx, t)
			slog.Warn("Deadline set:", "id", id, "ctx", d.ctx, "interval", t.Sub(time.Now()))
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
	return &RawPacketConn{ctx, tr,
		newContextDeadline("read with " + tr.Conn.LocalAddr().String()),
		newContextDeadline("write with " + tr.Conn.LocalAddr().String()),
	}
}

func (c *RawPacketConn) GetTransport() *quic.Transport {
	return c.tr
}

func (c *RawPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	ctx := c.readDeadline.ctx
	// slog.Debug("Reading non-QUIC packet", "ctx", ctx)
	return c.handleOperation(ctx,
		func(ctx context.Context) (n int, addr net.Addr, err error) {
			n, addr, err = c.tr.ReadNonQUICPacket(ctx, p)
			// slog.Debug("Read non-QUIC packet:", "ctx", ctx, "n", n, "addr", addr, "err", err)
			return
		},
	)
}

func (c *RawPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	ctx := c.writeDeadline.ctx
	// slog.Debug("Writing non-QUIC packet", "ctx", ctx, "addr", addr, "n", len(p))
	n, _, err := c.handleOperation(ctx,
		func(_ context.Context) (n int, _ net.Addr, err error) {
			n, err = c.tr.WriteTo(p, addr)
			// slog.Debug("Wrote non-QUIC packet:", "ctx", ctx, "n", n, "addr", addr, "err", err)
			return
		},
	)
	return n, err
}

func (c *RawPacketConn) handleOperation(
	ctx context.Context,
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
				slog.Warn("Non-QUIC packet op canceled", "ctx", ctx)
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

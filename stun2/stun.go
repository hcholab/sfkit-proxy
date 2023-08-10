package stun

import (
	"context"
	"fmt"
	"net"

	"github.com/pion/stun"
	"golang.org/x/exp/slog"
)

func Callback(ctx context.Context, b []byte, addr net.Addr) (err error) {
	slog.Default()
	if !stun.IsMessage(b) {
		err = fmt.Errorf("not a STUN packet: %s", b)
		return
	}
	slog.Debug("Received a STUN packet:", "bytes", string(b))
	return
}

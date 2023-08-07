package main

import (
	"context"
	"net/url"
	"os"

	"github.com/hcholab/sfkit-proxy/stun"
	"golang.org/x/exp/slog"
)

// type Subscriber interface {
// 	OnNATTypeChanged(natType NATType)
// 	OnExternalAddressChanged(address *Host, via string)
// }

type Subscriber struct{}

func (s *Subscriber) OnNATTypeChanged(natType stun.NATType) {
	slog.Info("OnNATTypeChanged(): ", natType)
}

func (s *Subscriber) OnExternalAddressChanged(address *stun.Host, via string) {
	slog.Info("OnExternalAddressChanged(): ", address.String(), "via", via)
}

func main() {
	ctx := context.Background()
	uri, err := url.Parse("quic://0.0.0.0:0")
	if err != nil {
		slog.Error("Problem parsing the URL")
		os.Exit(1)
	}
	cfg := &stun.Config{
		NATEnabled:          true,
		StunKeepaliveStartS: 180,
		StunKeepaliveMinS:   20,
		RawStunServers:      []string{"default"},
	}
	stun.Serve(ctx, uri, cfg, &Subscriber{})
}

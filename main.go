package main

import (
	"context"
	"flag"
	"log"
	"net/url"

	"github.com/hcholab/sfkit-proxy/quic"
	"golang.org/x/sync/errgroup"
)

// type Subscriber struct{}

// func (s *Subscriber) OnNATTypeChanged(natType stun.NATType) {
// 	slog.Info("OnNATTypeChanged():", "natType", natType)
// }

// func (s *Subscriber) OnExternalAddressChanged(address *stun.Host, via string) {
// 	slog.Info("OnExternalAddressChanged():", "address", address.String(), "via", via)
// }

func main() {
	var addr string
	flag.StringVar(&addr, "a", "udp://0.0.0.0:0", "Server URI")
	flag.Parse()

	uri, err := url.Parse(addr)
	if err != nil {
		log.Fatal("Error parsing server URI:", err)
	}

	ctx := context.Background()
	errs, ctx := errgroup.WithContext(ctx)

	quicSvc, err := quic.NewService(ctx, uri, errs)
	if err != nil {
		log.Fatal("Error in QUIC service: ", err)
	}
	defer quicSvc.Stop()

	// uri, err := url.Parse("quic://0.0.0.0:0")
	// if err != nil {
	// 	log.Fatal("Error parsing the URL: ", err)
	// }
	// cfg := &stun.Config{
	// 	NATEnabled:          true,
	// 	StunKeepaliveStartS: 180,
	// 	StunKeepaliveMinS:   20,
	// 	RawStunServers:      []string{"default"},
	// }
	// err = stun.Serve(ctx, uri, cfg, &Subscriber{})
	// if err != nil {
	// 	log.Fatal("Error in STUN service: ", err)
	// }

	if err = errs.Wait(); err != nil {
		log.Fatal(err)
	}
}

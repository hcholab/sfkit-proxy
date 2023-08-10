package main

import (
	"context"
	"flag"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hcholab/sfkit-proxy/logging"
	"github.com/hcholab/sfkit-proxy/quic"
	stun "github.com/hcholab/sfkit-proxy/stun2"
	"golang.org/x/exp/slog"
	"golang.org/x/sync/errgroup"
)

// type Subscriber struct{}

// func (s *Subscriber) OnNATTypeChanged(natType stun.NATType) {
// 	slog.Info("OnNATTypeChanged():", "natType", natType)
// }

// func (s *Subscriber) OnExternalAddressChanged(address *stun.Host, via string) {
// 	slog.Info("OnExternalAddressChanged():", "address", address.String(), "via", via)
// }

type Args struct {
	ListenURI      *url.URL
	StunServerURIs []stun.URI
	Verbose        bool
}

func parseArgs() (args Args, err error) {
	listenAddr := "udp://0.0.0.0:0"
	stunServers := strings.Join(stun.DefaultServers(), ",")

	flag.StringVar(&listenAddr, "a", listenAddr, "Server URI")
	flag.StringVar(&stunServers, "s", stunServers, "Comma-separated list of STUN server URIs, in the order of preference")
	flag.BoolVar(&args.Verbose, "v", false, "Verbose output")
	flag.Parse()

	if args.ListenURI, err = url.Parse(listenAddr); err != nil {
		return
	}
	args.StunServerURIs, err = stun.ParseServers(strings.Split(stunServers, ","))
	return
}

func main() {
	// a separate run() is used so that all deferred cleanups
	// have a chance to finish before os.Exit()
	os.Exit(run())
}

func run() (exitCode int) {
	args, err := parseArgs()
	if err != nil {
		log.Fatal(err)
	}

	logging.SetupDefault(args.Verbose)

	ctx, cancel := context.WithCancel(context.Background())
	errs, ctx := errgroup.WithContext(ctx)
	defer cancel()

	quicSvc, err := quic.NewService(ctx, args.ListenURI, errs)
	if err != nil {
		log.Fatal(err)
	}
	defer quicSvc.Stop()

	stunSvc, err := stun.NewService(ctx, args.StunServerURIs, errs, quicSvc.Connection())
	if err != nil {
		log.Fatal(err)
	}
	defer stunSvc.Stop()

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

	exitCh := handleSignals(ctx, cancel)
	if err = errs.Wait(); err != nil {
		log.Fatal(err)
	}
	return <-exitCh
}

func handleSignals(ctx context.Context, cancel context.CancelFunc) <-chan int {
	exitCh := make(chan int, 1)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case <-ctx.Done():
		case sig := <-sigCh:
			slog.Warn("Received", "signal", sig)
			cancel()
			if s, ok := sig.(syscall.Signal); ok {
				exitCh <- 128 + int(s)
			}
		}
	}()
	return exitCh
}

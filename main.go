package main

import (
	"context"
	"flag"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hcholab/sfkit-proxy/ice"
	"github.com/hcholab/sfkit-proxy/logging"
	"github.com/hcholab/sfkit-proxy/quic"
	"golang.org/x/exp/slog"
	"golang.org/x/sync/errgroup"
)

type Args struct {
	ListenURI       *url.URL
	SignalServerURI *url.URL
	StunServerURIs  []string
	StudyID         string
	Verbose         bool
}

func parseArgs() (args Args, err error) {
	listenURI := "udp://0.0.0.0:0"
	signalServerURI := "ws://host.docker.internal:8000/api/ice" // TODO change default for Terra
	stunServers := strings.Join(ice.DefaultSTUNServers(), ",")

	flag.StringVar(&signalServerURI, "api", signalServerURI, "ICE signaling server API")
	flag.StringVar(&listenURI, "listen", listenURI, "Local listener URI")
	flag.StringVar(&stunServers, "stun", stunServers, "Comma-separated list of STUN/TURN server URIs, in the order of preference")

	flag.StringVar(&args.StudyID, "study", "", "Study ID")
	flag.BoolVar(&args.Verbose, "v", false, "Verbose output")

	flag.Parse()

	if args.ListenURI, err = url.Parse(listenURI); err != nil {
		return
	}
	if args.SignalServerURI, err = url.Parse(signalServerURI); err != nil {
		return
	}
	args.StunServerURIs = strings.Split(stunServers, ",")
	if args.StunServerURIs[0] == "" {
		args.StunServerURIs = nil
	}
	return
}

func main() {
	exitCode, err := run()
	if err != nil && err != context.Canceled {
		slog.Error(err.Error())
	}
	os.Exit(exitCode)
}

func run() (exitCode int, err error) {
	exitCode = 1

	args, err := parseArgs()
	if err != nil {
		return
	}

	logging.SetupDefault(args.Verbose)

	ctx, cancel := context.WithCancel(context.Background())
	errs, ctx := errgroup.WithContext(ctx)
	defer cancel()

	quicSvc, err := quic.NewService(ctx, args.ListenURI, errs)
	if err != nil {
		return
	}
	defer quicSvc.Stop()

	iceSvc, err := ice.NewService(ctx, args.SignalServerURI, args.StunServerURIs, args.StudyID, quicSvc.Connection())
	if err != nil {
		return
	}
	defer iceSvc.Stop()

	exitCh := handleSignals(ctx, cancel)
	if err = errs.Wait(); err != nil {
		return
	}
	exitCode = <-exitCh
	return
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

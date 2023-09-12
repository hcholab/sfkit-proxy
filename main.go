package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/hcholab/sfkit-proxy/ice"
	"github.com/hcholab/sfkit-proxy/logging"
	"github.com/hcholab/sfkit-proxy/mpc"
	"github.com/hcholab/sfkit-proxy/proxy"
	"github.com/hcholab/sfkit-proxy/quic"
	"github.com/hcholab/sfkit-proxy/util"
)

type Args struct {
	ListenURI       *url.URL
	SignalServerURI *url.URL
	SocksListenURI  *url.URL
	MPCConfig       *mpc.Config
	StudyID         string
	StunServerURIs  []string
	Verbose         bool
}

func parseArgs() (args Args, err error) {
	listenURI := "udp://:0"
	socksListenURI := "tcp://:8000"
	signalServerURI := "ws://host.docker.internal:8000/api/ice" // TODO: change default for Terra
	stunServers := strings.Join(ice.DefaultSTUNServers(), ",")
	mpcConfigPath := "configGlobal.toml"
	mpcPID := 0

	flag.StringVar(&signalServerURI, "api", signalServerURI, "ICE signaling server API")
	flag.StringVar(&listenURI, "listen", listenURI, "Local listener URI")
	flag.StringVar(&socksListenURI, "socks", socksListenURI, "Local SOCKS listener URI")
	flag.StringVar(
		&stunServers,
		"stun",
		stunServers,
		"Comma-separated list of STUN/TURN server URIs, in the order of preference",
	)

	flag.StringVar(&mpcConfigPath, "mpc", mpcConfigPath, "Global MPC config path (.toml file)")
	flag.StringVar(&args.StudyID, "study", "", "Study ID")
	flag.IntVar(&mpcPID, "pid", mpcPID, "Numeric party ID")

	flag.BoolVar(&args.Verbose, "v", false, "Verbose output")
	flag.Parse()

	if args.ListenURI, err = url.Parse(listenURI); err != nil {
		return
	}
	if args.SocksListenURI, err = url.Parse(socksListenURI); err != nil {
		return
	} else if args.SocksListenURI.Scheme != "tcp" {
		err = fmt.Errorf("invalid SOCKS URI scheme: %s, expected: tcp", args.SocksListenURI.Scheme)
		return
	}
	if args.SignalServerURI, err = url.Parse(signalServerURI); err != nil {
		return
	}
	args.StunServerURIs = strings.Split(stunServers, ",")
	if args.StunServerURIs[0] == "" {
		args.StunServerURIs = nil
	}
	if args.StudyID == "" {
		err = fmt.Errorf("empty study ID")
		return
	}
	if mpcPID < 0 {
		err = fmt.Errorf("invalid party ID: %d, must be >=0", mpcPID)
		return
	}
	args.MPCConfig, err = mpc.ParseConfig(mpcConfigPath, mpc.PID(mpcPID))
	return
}

func main() {
	exitCode, err := run()
	if err != nil && err != context.Canceled {
		slog.Error(err.Error())
	}
	slog.Warn("Exit", "code", exitCode)
	os.Exit(exitCode)
}

func run() (exitCode int, err error) {
	exitCode = 1

	args, err := parseArgs()
	if err != nil {
		fmt.Println("ERROR:", err.Error())
		flag.Usage()
		return
	}

	logging.SetupDefault(args.Verbose)

	ctx, cancel := context.WithCancel(context.Background())
	errs, ctx := errgroup.WithContext(ctx)
	defer cancel()

	iceSvc, err := ice.NewService(ctx, args.SignalServerURI, args.StunServerURIs, args.StudyID, args.MPCConfig)
	if err != nil {
		return
	}
	defer util.Cleanup(&err, iceSvc.Stop)

	quicSvc, err := quic.NewService(args.MPCConfig, iceSvc.GetPacketConns)
	if err != nil {
		return
	}
	defer util.Cleanup(&err, quicSvc.Stop)

	proxySvc, err := proxy.NewService(ctx, args.SocksListenURI, args.MPCConfig, quicSvc.GetConns, errs)
	if err != nil {
		return
	}
	defer util.Cleanup(&err, proxySvc.Stop)

	// Wait for exit
	exitCh := handleSignals(ctx)
	doneCh := make(chan error)
	go func() {
		doneCh <- errs.Wait()
	}()
	select {
	case exitCode = <-exitCh:
		slog.Debug("Exit from a signal")
	case err = <-doneCh:
		slog.Debug("Abnormal exit from a service")
	}
	return
}

func handleSignals(ctx context.Context) <-chan int {
	exitCh := make(chan int, 1)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case <-ctx.Done():
		case sig := <-sigCh:
			slog.Warn("Received", "signal", sig)
			if s, ok := sig.(syscall.Signal); ok {
				exitCh <- 128 + int(s)
			}
		}
	}()
	return exitCh
}

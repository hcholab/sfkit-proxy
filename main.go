package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

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
	AuthKey         string
	StunServerURIs  []string
	Proto           string
	Verbose         bool
}

func parseArgs() (args Args, err error) {
	socksListenURI := "socks5://localhost:7080"
	signalServerURI := "ws://host.docker.internal:8000/api/ice" // TODO: change default for Terra
	stunServers := strings.Join(ice.DefaultSTUNServers(), ",")
	mpcConfigPath := "configGlobal.toml"
	mpcPID := 0
	proto := "udp4"

	flag.StringVar(&signalServerURI, "api", signalServerURI, "ICE signaling server API")
	flag.StringVar(&socksListenURI, "socks", socksListenURI, "Local SOCKS listener URI")
	flag.StringVar(&stunServers, "stun", stunServers, "Comma-separated list of STUN/TURN server URIs, in the order of preference")

	flag.StringVar(&mpcConfigPath, "mpc", mpcConfigPath, "Global MPC config path (.toml file)")
	flag.StringVar(&args.StudyID, "study", "", "Study ID")
	flag.IntVar(&mpcPID, "pid", mpcPID, "Numeric party ID")

	flag.StringVar(&args.AuthKey, "auth-key", "", "Auth key, if any")

	flag.StringVar(&proto, "proto", proto, "Peer-to-peer network protocol: udp4, udp6, or udp (dual-stack)")
	flag.BoolVar(&args.Verbose, "v", false, "Verbose output")
	flag.Parse()

	if args.SocksListenURI, err = url.Parse(socksListenURI); err != nil {
		return
	} else if args.SocksListenURI.Scheme != "socks5" {
		err = fmt.Errorf("invalid SOCKS URI scheme: %s, expected: socks5", args.SocksListenURI.Scheme)
		return
	} else {
		args.SocksListenURI.Scheme = "tcp"
	}
	if args.SignalServerURI, err = url.Parse(signalServerURI); err != nil {
		return
	}
	args.StunServerURIs = strings.Split(stunServers, ",")
	if args.StunServerURIs[0] == "" {
		args.StunServerURIs = nil
	}
	args.Proto = proto
	if !slices.Contains([]string{"udp4", "udp6", "udp"}, args.Proto) {
		err = fmt.Errorf("invalid proto value: %s, must be udp4, udp6, or udp", args.Proto)
		return
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
		slog.Error("main():", "err", err.Error())
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
	errs := make(chan error, 1)
	defer cancel()

	// channel to signal when all clients are connected over WebSocket,
	// before initiating proxy communication
	wsReady := make(chan any)

	iceSvc, err := ice.NewService(ctx, wsReady, args.SignalServerURI, args.StunServerURIs, args.Proto, args.AuthKey, args.StudyID, args.MPCConfig, errs)
	if err != nil {
		return
	}
	defer util.Cleanup(&err, iceSvc.Stop)

	quicSvc, err := quic.NewService(args.MPCConfig, iceSvc.GetTLSConfigs, errs)
	if err != nil {
		return
	}
	defer util.Cleanup(&err, quicSvc.Stop)

	proxySvcCh := make(chan *proxy.Service, 1)
	util.Go(ctx, errs, func() (err error) {
		proxySvc, err := proxy.NewService(ctx, wsReady, args.SocksListenURI, args.MPCConfig, quicSvc.GetConns, errs)
		if err == nil {
			proxySvcCh <- proxySvc
		}
		return
	})
	defer util.Cleanup(&err, func() (err error) {
		if len(proxySvcCh) > 0 {
			err = (<-proxySvcCh).Stop()
		}
		return
	})

	// Wait for exit
	exitCh := handleSignals(ctx)
	for {
		select {
		case exitCode = <-exitCh:
			slog.Error("Exit from a signal", "code", exitCode)
			return
		case err = <-errs:
			if err != nil {
				slog.Error("Abnormal exit from a service", "err", err)
				return
			}
		}
	}
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

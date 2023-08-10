// Copyright (C) 2019 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package stun

import (
	"context"
	"net"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/AudriusButkevicius/pfilter"
	"github.com/ccding/go-stun/stun"
)

const stunRetryInterval = 5 * time.Minute

var l = stun.NewLogger()

type Host = stun.Host
type NATType = stun.NATType

// NAT types.

const (
	NATError                = stun.NATError
	NATUnknown              = stun.NATUnknown
	NATNone                 = stun.NATNone
	NATBlocked              = stun.NATBlocked
	NATFull                 = stun.NATFull
	NATSymmetric            = stun.NATSymmetric
	NATRestricted           = stun.NATRestricted
	NATPortRestricted       = stun.NATPortRestricted
	NATSymmetricUDPFirewall = stun.NATSymmetricUDPFirewall
)

type writeTrackingUdpConn struct {
	// Needs to be UDPConn not PacketConn, as pfilter checks for WriteMsgUDP/ReadMsgUDP
	// and even if we embed UDPConn here, in place of a PacketConn, seems the interface
	// check fails.
	*net.UDPConn
	lastWrite atomic.Int64
}

func (c *writeTrackingUdpConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	c.lastWrite.Store(time.Now().Unix())
	return c.UDPConn.WriteTo(p, addr)
}

func (c *writeTrackingUdpConn) WriteMsgUDP(b, oob []byte, addr *net.UDPAddr) (n, oobn int, err error) {
	c.lastWrite.Store(time.Now().Unix())
	return c.UDPConn.WriteMsgUDP(b, oob, addr)
}

func (c *writeTrackingUdpConn) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	c.lastWrite.Store(time.Now().Unix())
	return c.UDPConn.WriteToUDP(b, addr)
}

func (c *writeTrackingUdpConn) Write(b []byte) (int, error) {
	c.lastWrite.Store(time.Now().Unix())
	return c.UDPConn.Write(b)
}

func (c *writeTrackingUdpConn) getLastWrite() time.Time {
	return time.Unix(c.lastWrite.Load(), 0)
}

// Subscriber/service

type Subscriber interface {
	OnNATTypeChanged(natType NATType)
	OnExternalAddressChanged(address *Host, via string)
}

type Service struct {
	name       string
	cfg        *Config
	subscriber Subscriber
	stunConn   net.PacketConn
	client     *stun.Client

	writeTrackingUdpConn *writeTrackingUdpConn

	natType NATType
	addr    *Host
}

func New(cfg *Config, subscriber Subscriber, conn *net.UDPConn) (*Service, net.PacketConn) {
	// Wrap the original connection to track writes on it
	writeTrackingUdpConn := &writeTrackingUdpConn{UDPConn: conn}

	// Wrap it in a filter and split it up, so that stun packets arrive on stun conn, others arrive on the data conn
	filterConn := pfilter.NewPacketFilter(writeTrackingUdpConn)
	otherDataConn := filterConn.NewConn(otherDataPriority, nil)
	stunConn := filterConn.NewConn(stunFilterPriority, &stunFilter{
		ids: make(map[string]time.Time),
	})

	filterConn.Start()

	// Construct the client to use the stun conn
	client := stun.NewClientWithConnection(stunConn)
	client.SetSoftwareName("") // Explicitly unset this, seems to freak some servers out.

	// Return the service and the other conn to the client
	name := "Stun@"
	if local := conn.LocalAddr(); local != nil {
		name += local.Network() + "://" + local.String()
	} else {
		name += "unknown"
	}
	s := &Service{
		name: name,

		cfg:        cfg,
		subscriber: subscriber,
		stunConn:   stunConn,
		client:     client,

		writeTrackingUdpConn: writeTrackingUdpConn,

		natType: NATUnknown,
		addr:    nil,
	}
	return s, otherDataConn
}

func (s *Service) Serve(ctx context.Context) error {
	defer func() {
		s.setNATType(NATUnknown)
		s.setExternalAddress(nil, "")
	}()

	// Closing s.stunConn unblocks operations that use the connection
	// (Discover, Keepalive) and might otherwise block us from returning.
	go func() {
		<-ctx.Done()
		_ = s.stunConn.Close()
	}()

	timer := time.NewTimer(time.Millisecond)

	for {
	disabled:
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}

		if s.cfg.IsStunDisabled() {
			timer.Reset(time.Second)
			continue
		}

		l.Debugf("Starting stun for %s", s)

		for _, addr := range s.cfg.StunServers() {
			// This blocks until we hit an exit condition or there are issues with the STUN server.
			// This returns a boolean signifying if a different STUN server should be tried (oppose to the whole thing
			// shutting down and this winding itself down.
			s.runStunForServer(ctx, addr)

			// Have we been asked to stop?
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// Are we disabled?
			if s.cfg.IsStunDisabled() {
				l.Infoln("STUN disabled")
				s.setNATType(NATUnknown)
				s.setExternalAddress(nil, "")
				goto disabled
			}

			// Unpunchable NAT? Chillout for some time.
			if !s.isCurrentNATTypePunchable() {
				break
			}
		}

		// We failed to contact all provided stun servers or the nat is not punchable.
		// Chillout for a while.
		timer.Reset(stunRetryInterval)
	}
}

func (s *Service) runStunForServer(ctx context.Context, addr string) {
	l.Debugf("Running stun for %s via %s", s, addr)

	// Resolve the address, so that in case the server advertises two
	// IPs, we always hit the same one, as otherwise, the mapping might
	// expire as we hit the other address, and cause us to flip flop
	// between servers/external addresses, as a result flooding discovery
	// servers.
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		l.Debugf("%s stun addr resolution on %s: %s", s, addr, err)
		return
	}
	s.client.SetServerAddr(udpAddr.String())

	var natType stun.NATType
	var extAddr *stun.Host
	err = callWithContext(ctx, func() error {
		natType, extAddr, err = s.client.Discover()
		return err
	})
	if err != nil || extAddr == nil {
		l.Debugf("%s stun discovery on %s: %s", s, addr, err)
		return
	}

	// The stun server is most likely borked, try another one.
	if natType == NATError || natType == NATUnknown || natType == NATBlocked {
		l.Debugf("%s stun discovery on %s resolved to %s", s, addr, natType)
		return
	}

	s.setNATType(natType)
	l.Debugf("%s detected NAT type: %s via %s", s, natType, addr)

	// We can't punch through this one, so no point doing keepalives
	// and such, just let the caller check the nat type and work it out themselves.
	if !s.isCurrentNATTypePunchable() {
		l.Debugf("%s cannot punch %s, skipping", s, natType)
		return
	}

	s.setExternalAddress(extAddr, addr)

	s.stunKeepAlive(ctx, addr, extAddr)
}

func callWithContext(ctx context.Context, fn func() error) error {
	var err error
	done := make(chan struct{})
	go func() {
		err = fn()
		close(done)
	}()
	select {
	case <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) stunKeepAlive(ctx context.Context, addr string, extAddr *Host) {
	var err error
	nextSleep := time.Duration(s.cfg.StunKeepaliveStartS) * time.Second

	l.Debugf("%s starting stun keepalive via %s, next sleep %s", s, addr, nextSleep)

	for {
		if areDifferent(s.addr, extAddr) {
			// If the port has changed (addresses are not equal but the hosts are equal),
			// we're probably spending too much time between keepalives, reduce the sleep.
			if s.addr != nil && extAddr != nil && s.addr.IP() == extAddr.IP() {
				nextSleep /= 2
				l.Debugf("%s stun port change (%s to %s), next sleep %s", s, s.addr.TransportAddr(), extAddr.TransportAddr(), nextSleep)
			}

			s.setExternalAddress(extAddr, addr)

			// The stun server is probably stuffed, we've gone beyond min timeout, yet the address keeps changing.
			minSleep := time.Duration(s.cfg.StunKeepaliveMinS) * time.Second
			if nextSleep < minSleep {
				l.Debugf("%s keepalive aborting, sleep below min: %s < %s", s, nextSleep, minSleep)
				return
			}
		}

		// Adjust the keepalives to fire only nextSleep after last write.
		lastWrite := s.writeTrackingUdpConn.getLastWrite()
		minSleep := time.Duration(s.cfg.StunKeepaliveMinS) * time.Second
		if nextSleep < minSleep {
			nextSleep = minSleep
		}
	tryLater:
		sleepFor := nextSleep

		timeUntilNextKeepalive := time.Until(lastWrite.Add(sleepFor))
		if timeUntilNextKeepalive > 0 {
			sleepFor = timeUntilNextKeepalive
		}

		l.Debugf("%s stun sleeping for %s", s, sleepFor)

		select {
		case <-time.After(sleepFor):
		case <-ctx.Done():
			l.Debugf("%s stopping, aborting stun", s)
			return
		}

		if s.cfg.IsStunDisabled() {
			// Disabled, give up
			l.Debugf("%s disabled, aborting stun ", s)
			return
		}

		// Check if any writes happened while we were sleeping, if they did, sleep again
		lastWrite = s.writeTrackingUdpConn.getLastWrite()
		if gap := time.Since(lastWrite); gap < nextSleep {
			l.Debugf("%s stun last write gap less than next sleep: %s < %s. Will try later", s, gap, nextSleep)
			goto tryLater
		}

		l.Debugf("%s stun keepalive", s)

		extAddr, err = s.client.Keepalive()
		if err != nil {
			l.Debugf("%s stun keepalive on %s: %s (%v)", s, addr, err, extAddr)
			return
		}
	}
}

func (s *Service) setNATType(natType NATType) {
	if natType != s.natType {
		l.Debugf("Notifying %s of NAT type change: %s", s.subscriber, natType)
		s.subscriber.OnNATTypeChanged(natType)
	}
	s.natType = natType
}

func (s *Service) setExternalAddress(addr *Host, via string) {
	if areDifferent(s.addr, addr) {
		l.Debugf("Notifying %s of address change: %s via %s", s.subscriber, addr, via)
		s.subscriber.OnExternalAddressChanged(addr, via)
	}
	s.addr = addr
}

func (s *Service) String() string {
	return s.name
}

func (s *Service) isCurrentNATTypePunchable() bool {
	return s.natType == NATNone || s.natType == NATPortRestricted || s.natType == NATRestricted || s.natType == NATFull || s.natType == NATSymmetricUDPFirewall
}

func areDifferent(first, second *Host) bool {
	if (first == nil) != (second == nil) {
		return true
	}
	if first != nil {
		return first.TransportAddr() != second.TransportAddr()
	}
	return false
}

func quicNetwork(uri *url.URL) string {
	switch uri.Scheme {
	case "quic4":
		return "udp4"
	case "quic6":
		return "udp6"
	default:
		return "udp"
	}
}

func Serve(ctx context.Context, uri *url.URL, cfg *Config, s Subscriber) error {
	l.SetDebug(true)
	l.Debugf("Serving %s, cfg: %+v", uri, cfg)

	network := quicNetwork(uri)

	udpAddr, err := net.ResolveUDPAddr(network, uri.Host)
	if err != nil {
		l.Infoln("Listen (BEP/quic):", err)
		return err
	}
	l.Debugf("Resolved UDP addr: %s", udpAddr)

	udpConn, err := net.ListenUDP(network, udpAddr)
	if err != nil {
		l.Infoln("Listen (BEP/quic):", err)
		return err
	}
	defer func() { _ = udpConn.Close() }()

	svc, conn := New(cfg, s, udpConn)
	defer conn.Close()

	svc.Serve(ctx)

	return nil
}
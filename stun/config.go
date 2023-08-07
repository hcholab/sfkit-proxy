// Copyright (C) 2019 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package stun

import (
	"math/rand"
	"strings"
)

type Config struct {
	NATEnabled          bool     `protobuf:"varint,14,opt,name=nat_traversal_enabled,json=natTraversalEnabled,proto3" json:"natEnabled" xml:"natEnabled" default:"true"`
	StunKeepaliveStartS int      `protobuf:"varint,43,opt,name=stun_keepalive_start_s,json=stunKeepaliveStartS,proto3,casttype=int" json:"stunKeepaliveStartS" xml:"stunKeepaliveStartS" default:"180"`
	StunKeepaliveMinS   int      `protobuf:"varint,44,opt,name=stun_keepalive_min_s,json=stunKeepaliveMinS,proto3,casttype=int" json:"stunKeepaliveMinS" xml:"stunKeepaliveMinS" default:"20"`
	RawStunServers      []string `protobuf:"bytes,45,rep,name=stun_servers,json=stunServers,proto3" json:"stunServers" xml:"stunServer" default:"default"`
}

var (
	// DefaultPrimaryStunServers are servers provided by us (to avoid causing the public servers burden)
	DefaultPrimaryStunServers = []string{
		"stun.syncthing.net:3478",
	}
	DefaultSecondaryStunServers = []string{
		"stun.callwithus.com:3478",
		"stun.counterpath.com:3478",
		"stun.counterpath.net:3478",
		"stun.ekiga.net:3478",
		"stun.ideasip.com:3478",
		"stun.internetcalls.com:3478",
		"stun.schlund.de:3478",
		"stun.sipgate.net:10000",
		"stun.sipgate.net:3478",
		"stun.voip.aebc.com:3478",
		"stun.voiparound.com:3478",
		"stun.voipbuster.com:3478",
		"stun.voipstunt.com:3478",
		"stun.xten.com:3478",
	}
)

func (c Config) IsStunDisabled() bool {
	return c.StunKeepaliveMinS < 1 || c.StunKeepaliveStartS < 1 || !c.NATEnabled
}

func (c Config) StunServers() []string {
	var addresses []string
	for _, addr := range c.RawStunServers {
		switch addr {
		case "default":
			defaultPrimaryAddresses := make([]string, len(DefaultPrimaryStunServers))
			copy(defaultPrimaryAddresses, DefaultPrimaryStunServers)
			shuffle(defaultPrimaryAddresses)
			addresses = append(addresses, defaultPrimaryAddresses...)

			defaultSecondaryAddresses := make([]string, len(DefaultSecondaryStunServers))
			copy(defaultSecondaryAddresses, DefaultSecondaryStunServers)
			shuffle(defaultSecondaryAddresses)
			addresses = append(addresses, defaultSecondaryAddresses...)
		default:
			addresses = append(addresses, addr)
		}
	}

	addresses = uniqueTrimmedStrings(addresses)

	return addresses
}

// Shuffle the order of elements in slice.
func shuffle[T any](s []T) {
	rand.Shuffle(len(s), func(i, j int) {
		s[i], s[j] = s[j], s[i]
	})
}

// uniqueTrimmedStrings returns a list of all unique strings in ss,
// in the order in which they first appear in ss, after trimming away
// leading and trailing spaces.
func uniqueTrimmedStrings(ss []string) []string {
	var m = make(map[string]struct{}, len(ss))
	var us = make([]string, 0, len(ss))
	for _, v := range ss {
		v = strings.Trim(v, " ")
		if _, ok := m[v]; ok {
			continue
		}
		m[v] = struct{}{}
		us = append(us, v)
	}

	return us
}

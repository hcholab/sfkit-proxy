package mpc

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"

	"github.com/BurntSushi/toml"
	"github.com/hcholab/sfgwas/gwas"

	_ "github.com/hcholab/sfkit-proxy/mpc/internal"
)

type PID int

type Config struct {
	// ServerPIDs maps remote server address:port to remote server PID
	ServerPIDs map[netip.AddrPort]PID

	// PIDServers maps remote server PID to remote server address:port(s)
	PIDServers map[PID][]netip.AddrPort

	// PIDClients maps remote client PID to local server address:port(s)
	PIDClients map[PID][]netip.AddrPort

	// PeerPIDs is a list of all remote PIDs that are
	// either clients or servers for the local PID
	PeerPIDs []PID

	LocalPID PID
	Threads  int
}

// IsControlling returns true iff the local ICE agent is controlling for the peer PID.
func (c *Config) IsControlling(peerPID PID) bool {
	return c.LocalPID > peerPID
}

// IsClient returns true iff the local PID is a client to the peer PID.
func (c *Config) IsClient(peerPID PID) bool {
	return len(c.PIDServers[peerPID]) > 0
}

// ParseConfig parses MPC TOML config file,
// and pre-calculates address <-> PID mappings for easy lookup.
func ParseConfig(configPath string, localPID PID) (c *Config, err error) {
	tc := &gwas.Config{}
	if _, err = toml.DecodeFile(configPath, tc); err != nil {
		return
	}
	c = &Config{
		LocalPID:   localPID,
		ServerPIDs: make(map[netip.AddrPort]PID),
		PIDServers: make(map[PID][]netip.AddrPort),
		PIDClients: make(map[PID][]netip.AddrPort),
		Threads:    tc.MpcNumThreads,
	}
	peerPIDs := make(map[PID]bool)
	for serverID, s := range tc.Servers {
		var serverPID PID
		if serverPID, err = parsePID(serverID); err != nil {
			return
		}
		for clientID, p := range s.Ports {
			var clientPID PID
			if clientPID, err = parsePID(clientID); err != nil {
				return
			}
			var ap netip.AddrPort
			host := fmt.Sprintf("%s:%s", s.IpAddr, p)
			if ap, err = netip.ParseAddrPort(host); err != nil {
				return
			}
			for t := 0; t < tc.MpcNumThreads; t++ {
				switch localPID {
				case serverPID:
					c.PIDClients[clientPID] = append(c.PIDClients[clientPID], ap)
					peerPIDs[clientPID] = true
				case clientPID:
					c.PIDServers[serverPID] = append(c.PIDServers[serverPID], ap)
					c.ServerPIDs[ap] = serverPID
					peerPIDs[serverPID] = true
				}
				ap = netip.AddrPortFrom(ap.Addr(), ap.Port()+1)
			}
		}
	}
	for pid := range peerPIDs {
		c.PeerPIDs = append(c.PeerPIDs, pid)
	}
	return
}

var pidRe = regexp.MustCompile(`\d+`)

func parsePID(id string) (pid PID, err error) {
	if match := pidRe.FindString(id); match == "" {
		err = fmt.Errorf("cannot parse PID from party ID: %s", id)
		return
	} else {
		var id int
		if id, err = strconv.Atoi(match); err == nil {
			pid = PID(id)
		}
	}
	return
}

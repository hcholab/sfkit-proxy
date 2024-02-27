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

	// PIDClients maps remote client PID to local server address:port
	PIDClients map[PID][]netip.AddrPort

	// PeerPIDs is a list of all remote PIDs that are
	// either clients or servers for the local PID
	PeerPIDs []PID

	LocalPID PID
	Threads  int
}

func (c *Config) IsClient(peerPID PID) bool {
	return c.LocalPID > peerPID
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
				switch true {
				case localPID == serverPID && !c.IsClient(clientPID):
					c.PIDClients[clientPID] = append(c.PIDClients[clientPID], ap)
					peerPIDs[clientPID] = true
				case localPID == clientPID && c.IsClient(serverPID):
					c.ServerPIDs[ap] = serverPID
					peerPIDs[serverPID] = true
				default:
					continue
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

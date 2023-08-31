package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"

	"github.com/BurntSushi/toml"
	"github.com/hcholab/sfgwas/gwas"
	"golang.org/x/net/proxy"
	"golang.org/x/sync/errgroup"
)

func main() {
	// parse mpcConfigPath
	mpcConfigPath := "configGlobal.toml"
	studyID := "1"
	selfID := "party" + os.Getenv("PID")

	flag.StringVar(&mpcConfigPath, "mpc", mpcConfigPath, "Global MPC config path (.toml file)")
	flag.StringVar(&studyID, "study", studyID, "Study ID")

	flag.Parse()

	mpc := &gwas.Config{}
	_, err := toml.DecodeFile(mpcConfigPath, mpc)
	if err != nil {
		return
	}
	fmt.Printf("MPC config: Threads=%d, Servers=%+v\n", mpc.MpcNumThreads, mpc.Servers)

	self := mpc.Servers[selfID]
	selfIP := net.ParseIP(self.IpAddr)

	errs := errgroup.Group{}
	p := 0

	// iterate over self ports and listen on each port..port+mpc.MpcNumThreads
	for id, port := range self.Ports {
		peerID := id
		p, err = strconv.Atoi(port)
		if err != nil {
			log.Fatalf("Error parsing port: %+v", err)
		}
		for i := 0; i < mpc.MpcNumThreads; i++ {
			addr := net.TCPAddr{IP: selfIP, Port: p + i}
			errs.Go(func() error {
				return listen(&addr, peerID)
			})
		}
	}

	for id, peer := range mpc.Servers {
		peerID := id
		peerIP := net.ParseIP(peer.IpAddr)
		if port, ok := peer.Ports[selfID]; ok {
			p, err = strconv.Atoi(port)
			if err != nil {
				log.Fatalf("Error parsing port: %+v", err)
			}
			for i := 0; i < mpc.MpcNumThreads; i++ {
				addr := net.TCPAddr{IP: peerIP, Port: p + i}
				errs.Go(func() error {
					return send(&addr, peerID, selfID)
				})
			}
		}
	}

	if err = errs.Wait(); err != nil {
		log.Fatal(err)
	}
	log.Println("All good!")
}

// listen on addr for peerID and respond with the same message
func listen(addr *net.TCPAddr, peerID string) (err error) {
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return
	}
	defer l.Close()

	log.Printf("Listening on %s for %s", addr.String(), peerID)

	var conn net.Conn
	conn, err = l.AcceptTCP()
	if err != nil {
		return err
	}
	defer conn.Close()

	buf := make([]byte, len(peerID))
	var n int
	if n, err = conn.Read(buf); err != nil {
		return
	}
	msg := string(buf)
	if msg != peerID {
		err = fmt.Errorf("unexpected message from %s: %s", addr, msg)
	}
	log.Printf("Received message on %s: %s", addr, msg)

	if n, err = conn.Write(buf[:n]); err != nil {
		return
	}
	log.Printf("Echoed message from %s: %s", addr, string(buf[:n]))
	return
}

// send selfID to the remote peer and verify echoed response
func send(addr *net.TCPAddr, peerID string, selfID string) (err error) {
	log.Printf("Dialing %s on %s", peerID, addr)
	dialer := proxy.FromEnvironment()
	conn, err := dialer.Dial(addr.Network(), addr.String())
	if err != nil {
		return
	}
	defer conn.Close()
	log.Printf("Established connection to %s", addr)

	buf := []byte(selfID)
	n, err := conn.Write(buf)
	if err != nil {
		return
	}
	log.Printf("Sent message to %s: %s", addr, string(buf[:n]))

	if n, err = conn.Read(buf); err != nil {
		return
	}
	msg := string(buf)
	if msg != selfID {
		err = fmt.Errorf("unexpected response from %s: %s", addr, msg)
	}
	log.Printf("Received message from %s: %s", addr, string(buf[:n]))
	return
}

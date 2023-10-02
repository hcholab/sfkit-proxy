package ice

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"

	"github.com/pion/ice/v3"
	"github.com/pion/stun/v2"
	"golang.org/x/exp/slices"
	"golang.org/x/net/websocket"
	"golang.org/x/sync/errgroup"

	"github.com/hcholab/sfkit-proxy/auth"
	"github.com/hcholab/sfkit-proxy/mpc"
	"github.com/hcholab/sfkit-proxy/util"
)

type Service struct {
	mpc      *mpc.Config
	ws       *websocket.Conn
	errs     *errgroup.Group
	studyID  string
	stunURIs []*stun.URI
}

type MessageType string

const (
	MessageTypeCandidate   MessageType = "candidate"
	MessageTypeCredential  MessageType = "credential"
	MessageTypeCertificate MessageType = "certificate"
	MessageTypeError       MessageType = "error"
)

type Message struct {
	StudyID   string      `json:"studyID"`
	Type      MessageType `json:"type"`
	Data      string      `json:"data"`
	SourcePID mpc.PID     `json:"sourcePID"`
	TargetPID mpc.PID     `json:"targetPID"`
}

type Credential struct {
	Ufrag string `json:"ufrag"`
	Pwd   string `json:"pwd"`
}

type Certificate struct {
	Addr string `json:"addr"`
	PEM  string `json:"pem"`
}

const (
	authHeader    = "Authorization"
	studyIDHeader = "X-MPC-Study-ID"

	idLen   = 15
	idRunes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

var defaultSTUNServers = []string{
	// TODO: can we rely on Google?
	"stun:stun.l.google.com:19302",
	// "stun:stun1.l.google.com:19302",
	// "stun:stun2.l.google.com:19302",
	// "stun:stun3.l.google.com:19302",
	// "stun:stun4.l.google.com:19302",

	// from Syncthing, should be reliable
	// "stun:stun.syncthing.net:3478",
	// "stun:stun.callwithus.com:3478",
	// "stun:stun.counterpath.com:3478",
	// "stun:stun.counterpath.net:3478",
	// "stun:stun.ekiga.net:3478",
	// "stun:stun.ideasip.com:3478",
	// "stun:stun.internetcalls.com:3478",
	// "stun:stun.schlund.de:3478",
	// "stun:stun.sipgate.net:10000",
	// "stun:stun.sipgate.net:3478",
	// "stun:stun.voip.aebc.com:3478",
	// "stun:stun.voiparound.com:3478",
	// "stun:stun.voipbuster.com:3478",
	// "stun:stun.voipstunt.com:3478",
	// "stun:stun.xten.com:3478",
}

func DefaultSTUNServers() []string {
	return slices.Clone(defaultSTUNServers)
}

func NewService(ctx context.Context, api *url.URL, rawStunURIs []string, studyID string, mpcConf *mpc.Config, errs *errgroup.Group) (s *Service, err error) {
	s = &Service{
		mpc:     mpcConf,
		studyID: studyID,
		errs:    errs,
	}

	// parse stun URIs
	s.stunURIs, err = parseStunURIs(rawStunURIs)
	if err != nil {
		return
	}

	// connect to the signaling API via WebSocket
	// and return once all clients are connected
	// and ready to initiate the ICE protocol
	//
	// TODO: implement reconnect
	if err = s.connectWebSocket(ctx, api, studyID); err != nil {
		return
	}

	slog.Debug("Started ICE service")
	return
}

// TLSConf wraps *tls.Config and net.Addr of remote peer
type TLSConf struct {
	*tls.Config
	RemoteAddr net.Addr
}

// GetTLSConfigs initiates the ICE protocol with a peer,
// and returns a *conn.PacketConn channel, which allows the client
// to subscribe to connections established by the protocol.
//
// Based on https://github.com/pion/ice/tree/master/examples/ping-pong
func (s *Service) GetTLSConfigs(ctx context.Context, peerPID mpc.PID, udpConn net.PacketConn) (_ <-chan *TLSConf, _ io.Closer, err error) {
	tlsConfs := make(chan *TLSConf, 1)
	peerCerts := make(chan *Certificate, 1)
	conns := make(chan net.Conn, 1)

	if peerPID == s.mpc.LocalPID {
		err = fmt.Errorf("cannot connect to self")
		return
	}

	// initialize the ICE agent
	a, err := createICEAgent(s.stunURIs, udpConn)
	if err != nil {
		return
	}

	// start listening for ICE signaling messages
	s.errs.Go(util.Retry(ctx, func() error {
		return s.handleSignals(ctx, a, conns, peerCerts)
	}))

	// generate TLS certificates and exhange them with peers
	s.errs.Go(util.Retry(ctx, func() error {
		return s.handleCerts(ctx, peerPID, peerCerts, conns, tlsConfs)
	}))

	// when we have gathered a new ICE Candidate, send it to the remote peer(s)
	if err = s.setupNewCandidateHandler(a, peerPID); err != nil {
		return
	}

	// handle ICE connection state changes
	if err = setupConnectionStateHandler(a); err != nil {
		return
	}

	// get the local auth credentials and send them to remote peer(s)
	if err = s.sendLocalCredentials(a, peerPID); err != nil {
		return
	}

	// start trickle ICE candidate gathering process
	slog.Debug("Gathering ICE candidates for", "peer", peerPID)
	return tlsConfs, a, a.GatherCandidates()
}

func (s *Service) connectWebSocket(ctx context.Context, api *url.URL, studyID string) (err error) {
	originURL := url.URL{Scheme: api.Scheme, Host: api.Host}
	wsConfig, err := websocket.NewConfig(api.String(), originURL.String())
	if err != nil {
		return
	}

	auth, err := getAuthHeader(ctx)
	if err != nil {
		return
	}
	h := wsConfig.Header
	h.Add(authHeader, auth)
	h.Add(studyIDHeader, studyID)

	slog.Info("Waiting for all parties to connect")
	s.ws, err = websocket.DialConfig(wsConfig)
	return
}

func createICEAgent(stunURIs []*stun.URI, udpConn net.PacketConn) (a *ice.Agent, err error) {
	a, err = ice.NewAgent(&ice.AgentConfig{
		Urls: stunURIs,
		NetworkTypes: []ice.NetworkType{
			ice.NetworkTypeUDP4,
			ice.NetworkTypeUDP6,
		},
		UDPMuxSrflx: ice.NewUniversalUDPMuxDefault(ice.UniversalUDPMuxParams{
			UDPConn: udpConn,
		}),
	})
	if err == nil {
		slog.Debug("Created ICE agent")
	}
	return
}

func parseStunURIs(rawURIs []string) (uris []*stun.URI, err error) {
	for _, u := range rawURIs {
		var uri *stun.URI
		uri, err = stun.ParseURI(u)
		if err != nil {
			return
		}
		uris = append(uris, uri)
	}
	return
}

func (s *Service) setupNewCandidateHandler(a *ice.Agent, targetPID mpc.PID) (err error) {
	if err = a.OnCandidate(func(c ice.Candidate) {
		if c == nil {
			return
		}
		slog.Debug("Gathered ICE candidate:", "candidate", c)

		msg := Message{
			// IDs are not authoritative, but useful for debugging
			SourcePID: s.mpc.LocalPID,
			StudyID:   s.studyID,
			TargetPID: targetPID,
			Type:      MessageTypeCandidate,
			Data:      c.Marshal(),
		}
		if e := websocket.JSON.Send(s.ws, msg); e != nil {
			slog.Error(err.Error())
		} else {
			slog.Debug("Sent ICE candidate:", "msg", msg)
		}
	}); err != nil {
		return
	}
	slog.Debug("Listening for ICE candidates")
	return
}

func setupConnectionStateHandler(a *ice.Agent) (err error) {
	// TODO: handle properly
	if err = a.OnConnectionStateChange(func(c ice.ConnectionState) {
		slog.Debug("ICE Connection State has changed", "state", c)
	}); err != nil {
		return
	}
	slog.Debug("Listening for ICE connection state changes")
	return
}

func (s *Service) sendLocalCredentials(a *ice.Agent, targetPID mpc.PID) (err error) {
	slog.Debug("Waiting for local ICE credentials", "targetPID", targetPID)
	localUfrag, localPwd, err := a.GetLocalUserCredentials()
	if err != nil {
		err = fmt.Errorf("getting local ICE credentials: %s", err.Error())
		return
	}
	slog.Debug("Obtained local ICE credentials:", "localUfrag", localUfrag, "localPwd", localPwd)
	cred, err := json.Marshal(Credential{
		Ufrag: localUfrag,
		Pwd:   localPwd,
	})
	if err != nil {
		err = fmt.Errorf("marshaling ICE credential: %s", err.Error())
		return
	}
	if err = websocket.JSON.Send(s.ws, Message{
		// Source and Study IDs are not authoritative, but useful for debugging
		SourcePID: s.mpc.LocalPID,
		StudyID:   s.studyID,
		TargetPID: targetPID,
		Type:      MessageTypeCredential,
		Data:      string(cred),
	}); err != nil {
		err = fmt.Errorf("sending local ICE credentials: %s", err.Error())
		return
	}
	slog.Debug("Sent local ICE credential(s)", "cred", string(cred))
	return
}

func (s *Service) handleCerts(ctx context.Context, peerPID mpc.PID, peerCerts <-chan *Certificate, conns <-chan net.Conn, tlsConfs chan<- *TLSConf) error {
	peerToLocalCerts := make(map[string]*tls.Certificate)
	peerToRemoteCerts := make(map[string]*Certificate)

	return util.Retry(ctx, func() (err error) {
		var peerAddr net.Addr
		select {
		case conn := <-conns:
			var localCert tls.Certificate
			if peerAddr, localCert, err = s.generateConnCert(conn, peerPID); err != nil {
				break
			}
			peerToLocalCerts[peerAddr.String()] = &localCert
			slog.Debug("Added local certificate for", "localAddr", conn.LocalAddr(), "peerAddr", peerAddr)

		case peerCert := <-peerCerts:
			if peerAddr, err = net.ResolveUDPAddr("udp", peerCert.Addr); err != nil {
				return
			}
			peerToRemoteCerts[peerCert.Addr] = peerCert
			slog.Debug("Added remote certificate for", "peerAddr", peerAddr)

		case <-ctx.Done():
			return ctx.Err()
		}
		if err != nil {
			slog.Error(err.Error())
		}

		// Check when both certs have been received
		localCert := peerToLocalCerts[peerAddr.String()]
		peerCert := peerToRemoteCerts[peerAddr.String()]
		if localCert == nil || peerCert == nil {
			return
		}
		tlsConf, err := getTLSConfig(s.mpc.IsClient(peerPID), localCert, peerCert)
		if err != nil {
			return
		}
		tlsConfs <- &TLSConf{Config: tlsConf, RemoteAddr: peerAddr}
		slog.Debug("Created TLS config for", "peerAddr", peerAddr)
		return
	})()
}

func (s *Service) generateConnCert(conn net.Conn, peerPID mpc.PID) (peerAddr net.Addr, localCert tls.Certificate, err error) {
	peerAddr = conn.RemoteAddr()
	localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		err = fmt.Errorf("cannot convert %s to UDPAddr: %s", conn.LocalAddr(), err)
		return
	}

	var certPEM string
	if localCert, certPEM, err = generateTLSCert(localAddr.IP); err != nil {
		return
	}
	slog.Debug("Generated local certificate for", "localAddr", localAddr, "peerPID", peerPID)

	cert, err := json.Marshal(Certificate{
		Addr: localAddr.String(),
		PEM:  certPEM,
	})
	if err != nil {
		err = fmt.Errorf("serializing certificate with addr=%s PEM=%s: %s", localAddr, certPEM, err)
		return
	}

	msg := Message{
		// IDs are not authoritative, but useful for debugging
		SourcePID: s.mpc.LocalPID,
		StudyID:   s.studyID,
		TargetPID: peerPID,
		Type:      MessageTypeCertificate,
		Data:      string(cert),
	}
	if err = websocket.JSON.Send(s.ws, msg); err == nil {
		slog.Debug("Sent local certificate for", "localAddr", localAddr, "peerAddr", peerAddr, "peerPID", peerPID)
	}
	return
}

type ConnOp func(context.Context, string, string) (net.Conn, error)

func (s *Service) handleSignals(ctx context.Context, a *ice.Agent, conns chan<- net.Conn, peerCerts chan<- *Certificate) (err error) {
	var msg Message
	if err = websocket.JSON.Receive(s.ws, &msg); err != nil {
		if err == io.EOF {
			return
		} else if errors.Is(err, net.ErrClosed) {
			return util.Permanent(err)
		}
		slog.Error("Receiving signaling message", "err", err)
		err = nil // TODO: do not ignore ?
		return
	}
	slog.Debug("Received signaling message:", "msg", msg)

	switch t := msg.Type; t {
	case MessageTypeCandidate:
		handleRemoteCandidate(a, msg.Data)
	case MessageTypeCredential:
		go s.handleRemoteCredential(a, msg, conns)
	case MessageTypeCertificate:
		handleRemoteCertificate(msg.Data, peerCerts)
	case MessageTypeError:
		slog.Error("Signaling error: " + msg.Data)
	default:
		slog.Error("Unknown", "msg.Type", t, "msg", msg)
	}
	return
}

func handleRemoteCandidate(a *ice.Agent, candidate string) {
	c, err := ice.UnmarshalCandidate(candidate)
	if err != nil {
		slog.Error("UnmarshalCandidate:", "err", err)
		return
	}
	if err = a.AddRemoteCandidate(c); err != nil {
		slog.Error("AddRemoteCandidate:", "err", err)
		return
	}
	slog.Debug("Added remote", "candidate", c)
}

func (s *Service) handleRemoteCredential(a *ice.Agent, msg Message, conns chan<- net.Conn) {
	var cred Credential
	var err error
	if err = json.Unmarshal([]byte(msg.Data), &cred); err != nil {
		slog.Error("Unmarshalling remote credential:", "msg.Data", msg.Data)
		return
	}
	slog.Debug("Obtained remote", "cred", cred)

	iceOp := a.Accept
	if s.mpc.IsClient(msg.SourcePID) {
		slog.Debug("Dialing ICE candidate", "remotePID", msg.SourcePID)
		iceOp = a.Dial
	} else {
		slog.Debug("Accepting ICE connection:", "remotePID", msg.SourcePID)
	}
	c, err := iceOp(context.TODO(), cred.Ufrag, cred.Pwd)
	if err != nil {
		slog.Error("ICE operation:", "err", err)
		return
	}

	slog.Info("Established ICE connection:", "localAddr", c.LocalAddr(), "remoteAddr", c.RemoteAddr())
	conns <- c
}

func handleRemoteCertificate(cert string, peerCerts chan<- *Certificate) {
	var c Certificate
	err := json.Unmarshal([]byte(cert), &c)
	if err != nil {
		slog.Error("Unmarshalling remote certificate:", "cert", cert)
		return
	}
	peerCerts <- &c
}

func getAuthHeader(ctx context.Context) (header string, err error) {
	token, err := auth.GetDefaultCredentialToken(ctx)
	if err != nil {
		return
	}
	header = "Bearer " + token
	return
}

func (s *Service) Stop() (err error) {
	slog.Warn("Stopping ICE service")
	return s.ws.Close()
}

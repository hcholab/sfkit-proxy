package ice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"

	"github.com/pion/ice/v2"
	"github.com/pion/stun"
	"golang.org/x/exp/slices"
	"golang.org/x/net/websocket"

	"github.com/hcholab/sfkit-proxy/auth"
	"github.com/hcholab/sfkit-proxy/conn"
	"github.com/hcholab/sfkit-proxy/mpc"
)

type Service struct {
	mpc      *mpc.Config
	ws       *websocket.Conn
	studyID  string
	stunURIs []*stun.URI
}

type MessageType string

const (
	MessageTypeCandidate  MessageType = "candidate"
	MessageTypeCredential MessageType = "credential"
	MessageTypeError      MessageType = "error"
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

func NewService(
	ctx context.Context,
	api *url.URL,
	rawStunURIs []string,
	studyID string,
	mpcConf *mpc.Config,
) (s *Service, err error) {
	s = &Service{
		mpc:     mpcConf,
		studyID: studyID,
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

// GetPacketConns initiates the ICE protocol with a peer,
// and returns a *conn.PacketConn channel, which allows the client
// to subscribe to connections established by the protocol.
//
// Based on https://github.com/pion/ice/tree/master/examples/ping-pong
func (s *Service) GetPacketConns(
	ctx context.Context,
	peerPID mpc.PID,
) (_ <-chan *conn.PacketConn, _ io.Closer, err error) {
	conns := make(chan *conn.PacketConn, 1)

	if peerPID == s.mpc.LocalPID {
		err = fmt.Errorf("cannot connect to self")
		return
	}

	// initialize the ICE agent
	a, err := createICEAgent(s.stunURIs)
	if err != nil {
		return
	}

	// start listening for ICE signaling messages
	go s.listenForSignals(ctx, a, conns)

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
	return conns, a, a.GatherCandidates()
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

func createICEAgent(stunURIs []*stun.URI) (a *ice.Agent, err error) {
	a, err = ice.NewAgent(&ice.AgentConfig{
		Urls: stunURIs,
		NetworkTypes: []ice.NetworkType{
			ice.NetworkTypeUDP4,
			ice.NetworkTypeUDP6,
		},
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

type IceOp func(context.Context, string, string) (*ice.Conn, error)

func (s *Service) listenForSignals(
	ctx context.Context,
	a *ice.Agent,
	conns chan<- *conn.PacketConn,
) {
	for {
		var msg Message
		if err := websocket.JSON.Receive(s.ws, &msg); err != nil {
			if err == io.EOF {
				// TODO: implement reconnect
				return
			} else if ctx.Err() == context.Canceled {
				return
			}
			slog.Error("Receiving signaling message", "err", err)
			continue
		}
		slog.Debug("Received signaling message:", "msg", msg)

		switch t := msg.Type; t {
		case MessageTypeCandidate:
			handleRemoteCandidate(a, msg.Data)
		case MessageTypeCredential:
			go s.handleRemoteCredential(a, msg, conns)
		case MessageTypeError:
			slog.Error("Signaling error: " + msg.Data)
		default:
			slog.Error("Unknown", "msg.Type", t, "msg", msg)
		}
	}
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

func (s *Service) handleRemoteCredential(a *ice.Agent, msg Message, conns chan<- *conn.PacketConn) {
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
	conns <- &conn.PacketConn{Conn: c}
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

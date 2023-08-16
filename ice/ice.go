package ice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/hcholab/sfkit-proxy/auth"
	"github.com/pion/ice/v2"
	"github.com/pion/stun"
	"golang.org/x/exp/slices"
	"golang.org/x/exp/slog"
	"golang.org/x/net/websocket"
)

type Service struct {
	a       *ice.Agent
	cid     string
	studyID string
	ws      *websocket.Conn
	conn    *ice.Conn
}

type MessageType string

const (
	MessageTypeStudy      MessageType = "study"
	MessageTypeConnected  MessageType = "connected"
	MessageTypeCandidate  MessageType = "candidate"
	MessageTypeCredential MessageType = "credential"
	MessageTypeError      MessageType = "error"
)

type Message struct {
	ClientID string      `json:"clientId"`
	StudyID  string      `json:"studyId"`
	Type     MessageType `json:"type"`
	Data     string      `json:"data"`
}

type Credential struct {
	Ufrag string `json:"ufrag"`
	Pwd   string `json:"pwd"`
}

const (
	idLen   = 15
	idRunes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

var (
	defaultSTUNServers = []string{
		// TODO can we rely on Google?
		"stun:stun.l.google.com:19302",
		"stun:stun1.l.google.com:19302",
		"stun:stun2.l.google.com:19302",
		"stun:stun3.l.google.com:19302",
		"stun:stun4.l.google.com:19302",

		// from Syncthing, should be reliable
		"stun:stun.syncthing.net:3478",
		"stun:stun.callwithus.com:3478",
		"stun:stun.counterpath.com:3478",
		"stun:stun.counterpath.net:3478",
		"stun:stun.ekiga.net:3478",
		"stun:stun.ideasip.com:3478",
		"stun:stun.internetcalls.com:3478",
		"stun:stun.schlund.de:3478",
		"stun:stun.sipgate.net:10000",
		"stun:stun.sipgate.net:3478",
		"stun:stun.voip.aebc.com:3478",
		"stun:stun.voiparound.com:3478",
		"stun:stun.voipbuster.com:3478",
		"stun:stun.voipstunt.com:3478",
		"stun:stun.xten.com:3478",
	}
)

func DefaultSTUNServers() []string {
	return slices.Clone(defaultSTUNServers)
}

// Based on https://github.com/pion/ice/tree/master/examples/ping-pong
func NewService(ctx context.Context, api *url.URL, stunURIs []string, studyID string) (s *Service, err error) {
	s = &Service{studyID: studyID}

	// connect to the signaling API via WebSocket
	// TODO implement reconnect
	if err = s.connectWebSocket(ctx, api); err != nil {
		return
	}

	// send study ID to the signaling server
	if err = s.sendStudyID(); err != nil {
		return
	}

	// receive client ID from the signaling server;
	// this also ensures all clients are now connected,
	// and are thus ready to initiate the ICE protocol
	if err = s.getClientID(); err != nil {
		return
	}

	// initialize the ICE agent
	if err = s.createICEAgent(stunURIs); err != nil {
		return
	}

	// start listening for ICE signaling messages
	go s.listenForSignals(ctx)

	// when we have gathered a new ICE Candidate, send it to the remote peer(s)
	if err = s.setupNewCandidateHandler(); err != nil {
		return
	}

	// handle ICE connection state changes
	if err = s.setupConnectionStateHandler(); err != nil {
		return
	}

	// get the local auth credentials and send them to remote peer(s)
	if err = s.sendLocalCredentials(); err != nil {
		return
	}

	// start trickle ICE candidate gathering process
	err = s.a.GatherCandidates()
	return
}

func (s *Service) connectWebSocket(ctx context.Context, api *url.URL) (err error) {
	originURL := url.URL{Scheme: api.Scheme, Host: api.Host}
	wsConfig, err := websocket.NewConfig(api.String(), originURL.String())
	if err != nil {
		return
	}
	authHeader, err := getAuthHeader(ctx)
	if err != nil {
		return
	}
	wsConfig.Header.Add("Authorization", authHeader)
	s.ws, err = websocket.DialConfig(wsConfig)
	return
}

func (s *Service) sendStudyID() (err error) {
	if err = websocket.JSON.Send(s.ws, Message{
		Type:    MessageTypeStudy,
		StudyID: s.studyID,
	}); err != nil {
		err = fmt.Errorf("sending study ID: %s", err.Error())
		return
	}
	slog.Debug("Sent study ID")
	return
}

func (s *Service) isControlling(remoteCID string) bool {
	slog.Debug("Comparing client IDs:", "local", s.cid, "remote", remoteCID)
	return s.cid > remoteCID
}

func (s *Service) getClientID() (err error) {
	var msg Message
	if err = websocket.JSON.Receive(s.ws, &msg); err != nil {
		err = fmt.Errorf("receiving Connected message: %s", err.Error())
		return
	}
	switch msg.Type {
	case MessageTypeConnected: // good
	case MessageTypeError:
		err = fmt.Errorf("receiving Connected message: %s", msg.Data)
		return
	default:
		err = fmt.Errorf("unexpected Connected message type: %s, msg: %+v", msg.Type, msg)
		return
	}
	s.cid = msg.ClientID
	slog.Debug("Obtained:", "clientID", s.cid)
	return
}

func (s *Service) createICEAgent(rawStunURIs []string) (err error) {
	urls, err := parseStunURIs(rawStunURIs)
	if err != nil {
		return
	}
	s.a, err = ice.NewAgent(&ice.AgentConfig{
		Urls:         urls,
		NetworkTypes: []ice.NetworkType{ice.NetworkTypeUDP4},
	})
	if err != nil {
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

func (s *Service) setupNewCandidateHandler() (err error) {
	if err = s.a.OnCandidate(func(c ice.Candidate) {
		if c == nil {
			return
		}
		slog.Debug("Gathered ICE candidate:", "candidate", c)
		msg := Message{
			ClientID: s.cid, // not authoritative, but useful for debugging
			StudyID:  s.studyID,
			Type:     MessageTypeCandidate,
			Data:     c.Marshal(),
		}
		if err := websocket.JSON.Send(s.ws, msg); err != nil {
			slog.Error(err.Error())
		} else {
			slog.Debug("Sent signaling message:", "msg", msg)
		}
	}); err != nil {
		return
	}
	slog.Debug("Listening for ICE candidates")
	return
}

func (s *Service) setupConnectionStateHandler() (err error) {
	// TODO handle properly
	if err = s.a.OnConnectionStateChange(func(c ice.ConnectionState) {
		slog.Info("ICE Connection State has changed", "state", c)
	}); err != nil {
		return
	}
	slog.Debug("Listening for ICE connection state changes")
	return
}

func (s *Service) sendLocalCredentials() (err error) {
	localUfrag, localPwd, err := s.a.GetLocalUserCredentials()
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
		ClientID: s.cid, // not authoritative, but useful for debugging
		StudyID:  s.studyID,
		Type:     MessageTypeCredential,
		Data:     string(cred),
	}); err != nil {
		err = fmt.Errorf("sending local ICE credentials: %s", err.Error())
		return
	}
	slog.Debug("Sent local ICE credential(s)", "cred", string(cred))
	return
}

type IceOp func(context.Context, string, string) (*ice.Conn, error)

func (s *Service) listenForSignals(ctx context.Context) {
	for {
		var msg Message
		if err := websocket.JSON.Receive(s.ws, &msg); err != nil {
			slog.Error("Receiving signaling message", "err", err)
			if err == io.EOF {
				// TODO implement reconnect
				return
			} else if ctx.Err() == context.Canceled {
				return
			}
			continue
		}
		slog.Debug("Received signaling message:", "msg", msg)

		switch t := msg.Type; t {
		case MessageTypeCandidate:
			s.handleRemoteCandidate(msg)

		case MessageTypeCredential:
			// TODO offload through a channel, to ensure processing order
			go s.handleRemoteCredential(msg)
		default:
			slog.Error("Unknown", "msg.Type", t, "msg", msg)
		}
	}
}

func (s *Service) handleRemoteCandidate(msg Message) {
	c, err := ice.UnmarshalCandidate(msg.Data)
	if err != nil {
		slog.Error("UnmarshalCandidate:", "err", err)
		return
	}
	if err = s.a.AddRemoteCandidate(c); err != nil {
		slog.Error("AddRemoteCandidate:", "err", err)
		return
	}
	slog.Debug("Added remote", "candidate", c)
	return
}

func (s *Service) handleRemoteCredential(msg Message) {
	var cred Credential
	var err error
	if err = json.Unmarshal([]byte(msg.Data), &cred); err != nil {
		slog.Error("Unmarshalling remote credential:", "msg.Data", msg.Data)
		return
	}
	slog.Debug("Obtained remote", "cred", cred)
	iceOp := s.a.Accept
	if s.isControlling(msg.ClientID) {
		slog.Debug("Dialing ICE candidate", "remoteID", msg.ClientID)
		iceOp = s.a.Dial
	} else {
		slog.Debug("Accepting ICE connection:", "remoteID", msg.ClientID)
	}
	if s.conn, err = iceOp(context.TODO(), cred.Ufrag, cred.Pwd); err != nil {
		slog.Error("ICE operation:", "err", err)
		return
	}
	slog.Debug("Established ICE connection:", "localAddr", s.conn.LocalAddr(), "remoteAddr", s.conn.RemoteAddr())
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
	errs := make([]string, 0, 2)
	if err = s.a.Close(); err != nil {
		errs = append(errs, err.Error())
	}
	if err = s.ws.Close(); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		err = fmt.Errorf("stopping ICE service: %s", strings.Join(errs, "; "))
		slog.Error(err.Error())
	}
	return
}

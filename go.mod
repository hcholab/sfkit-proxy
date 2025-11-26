module github.com/hcholab/sfkit-proxy

go 1.24

toolchain go1.24.3

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.18.0
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.10.1
	github.com/BurntSushi/toml v1.5.0
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/cenkalti/backoff/v4 v4.3.0
	github.com/fatih/color v1.18.0
	github.com/hcholab/sfgwas v0.0.0-20250319202214-5182aa7be703
	github.com/pion/ice/v3 v3.0.16
	github.com/pion/logging v0.2.3
	github.com/pion/stun/v2 v2.0.0
	github.com/quic-go/quic-go v0.57.1
	golang.org/x/exp v0.0.0-20250606033433-dcc06ee1d476
	golang.org/x/net v0.43.0
	golang.org/x/oauth2 v0.30.0
)

replace (
	github.com/armon/go-socks5 => github.com/howmp/go-socks5 v0.0.0-20220913003715-7c30c75ec0a2
	github.com/ldsec/lattigo/v2 => github.com/hcholab/lattigo/v2 v2.1.2-0.20220628190737-bde274261547
	github.com/pion/ice/v3 => github.com/hcholab/ice/v3 v3.0.0-20240408160803-88f88364298a
	go.dedis.ch/onet/v3 => github.com/hcholab/onet/v3 v3.0.0-20230828232509-90c2e1097481
)

require (
	cloud.google.com/go/compute/metadata v0.7.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.11.1 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.4.2 // indirect
	github.com/aead/chacha20 v0.0.0-20180709150244-8b13a72661da // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/daviddengcn/go-colortext v1.0.0 // indirect
	github.com/fanliao/go-concurrentMap v0.0.0-20141114143905-7d2d7a5ea67b // indirect
	github.com/golang-jwt/jwt/v5 v5.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/hhcho/frand v1.3.1-0.20210217213629-f1c60c334950 // indirect
	github.com/hhcho/mpc-core v0.0.0-20240903135357-56a83f968a6a // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/ldsec/lattigo/v2 v2.4.1 // indirect
	github.com/ldsec/unlynx v1.4.3 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/montanaflynn/stats v0.7.1 // indirect
	github.com/pion/dtls/v2 v2.2.12 // indirect
	github.com/pion/mdns/v2 v2.0.7 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/transport/v2 v2.2.10 // indirect
	github.com/pion/transport/v3 v3.0.7 // indirect
	github.com/pion/turn/v3 v3.0.3 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	go.dedis.ch/fixbuf v1.0.3 // indirect
	go.dedis.ch/kyber/v3 v3.1.0 // indirect
	go.dedis.ch/onet/v3 v3.2.10 // indirect
	go.dedis.ch/protobuf v1.0.11 // indirect
	go.etcd.io/bbolt v1.4.1 // indirect
	golang.org/x/crypto v0.41.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da // indirect
	gonum.org/v1/gonum v0.16.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	rsc.io/goversion v1.2.0 // indirect
)

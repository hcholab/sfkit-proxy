module github.com/hcholab/sfkit-proxy

go 1.21

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.7.2
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.3.1
	github.com/BurntSushi/toml v1.3.2
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/cenkalti/backoff/v4 v4.2.1
	github.com/fatih/color v1.15.0
	github.com/hcholab/sfgwas v0.0.0-20230721173306-041802b71401
	github.com/pion/ice/v3 v3.0.1
	github.com/pion/stun/v2 v2.0.0
	github.com/quic-go/quic-go v0.39.0
	golang.org/x/exp v0.0.0-20230905200255-921286631fa9
	golang.org/x/net v0.15.0
	golang.org/x/oauth2 v0.12.0
	golang.org/x/sync v0.3.0
)

replace (
	github.com/armon/go-socks5 => github.com/howmp/go-socks5 v0.0.0-20220913003715-7c30c75ec0a2
	github.com/ldsec/lattigo/v2 => github.com/hcholab/lattigo/v2 v2.1.2-0.20220628190737-bde274261547
	github.com/pion/ice/v3 => github.com/hcholab/ice/v3 v3.0.0-20231002171340-e6ed8ff23d5c
	go.dedis.ch/onet/v3 => github.com/hcholab/onet/v3 v3.0.0-20230828232509-90c2e1097481
)

require (
	cloud.google.com/go/compute v1.23.0 // indirect
	cloud.google.com/go/compute/metadata v0.2.3 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.3.0 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.2.0 // indirect
	github.com/aead/chacha20 v0.0.0-20180709150244-8b13a72661da // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/daviddengcn/go-colortext v1.0.0 // indirect
	github.com/fanliao/go-concurrentMap v0.0.0-20141114143905-7d2d7a5ea67b // indirect
	github.com/go-task/slim-sprig v0.0.0-20230315185526-52ccab3ef572 // indirect
	github.com/golang-jwt/jwt/v5 v5.0.0 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/pprof v0.0.0-20230926050212-f7f687d19a98 // indirect
	github.com/google/uuid v1.3.1 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/hhcho/frand v1.3.1-0.20210217213629-f1c60c334950 // indirect
	github.com/hhcho/mpc-core v0.0.0-20220828210829-24cf7abd1073 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/ldsec/lattigo/v2 v2.4.0 // indirect
	github.com/ldsec/unlynx v1.4.3 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/montanaflynn/stats v0.7.1 // indirect
	github.com/onsi/ginkgo/v2 v2.12.1 // indirect
	github.com/pion/dtls/v2 v2.2.7 // indirect
	github.com/pion/logging v0.2.2 // indirect
	github.com/pion/mdns v0.0.9 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/transport/v2 v2.2.4 // indirect
	github.com/pion/transport/v3 v3.0.1 // indirect
	github.com/pion/turn/v3 v3.0.1 // indirect
	github.com/pkg/browser v0.0.0-20210911075715-681adbf594b8 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/quic-go/qtls-go1-20 v0.3.4 // indirect
	github.com/stretchr/testify v1.8.4 // indirect
	go.dedis.ch/fixbuf v1.0.3 // indirect
	go.dedis.ch/kyber/v3 v3.1.0 // indirect
	go.dedis.ch/onet/v3 v3.2.10 // indirect
	go.dedis.ch/protobuf v1.0.11 // indirect
	go.etcd.io/bbolt v1.3.7 // indirect
	go.uber.org/mock v0.3.0 // indirect
	golang.org/x/crypto v0.13.0 // indirect
	golang.org/x/mod v0.12.0 // indirect
	golang.org/x/sys v0.12.0 // indirect
	golang.org/x/text v0.13.0 // indirect
	golang.org/x/tools v0.13.0 // indirect
	golang.org/x/xerrors v0.0.0-20220907171357-04be3eba64a2 // indirect
	gonum.org/v1/gonum v0.14.0 // indirect
	google.golang.org/appengine v1.6.8 // indirect
	google.golang.org/protobuf v1.31.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	rsc.io/goversion v1.2.0 // indirect
)

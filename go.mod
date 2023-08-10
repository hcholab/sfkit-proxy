module github.com/hcholab/sfkit-proxy

go 1.20

require (
	github.com/AudriusButkevicius/pfilter v0.0.11
	github.com/ccding/go-stun/stun v0.0.0-20200514191101-4dc67bcdb029

	// Use draft quic-go implementation to support UDP multiplexing
	// https://github.com/quic-go/quic-go/pull/3992/commits
	github.com/quic-go/quic-go v0.37.1-0.20230802030815-6f12cce1462a

	// TODO replace with stdlib slog after migrating to Go 1.21
	golang.org/x/exp v0.0.0-20230522175609-2e198f4a06a1
	golang.org/x/sync v0.2.0
)

require (
	github.com/go-task/slim-sprig v0.0.0-20230315185526-52ccab3ef572 // indirect
	github.com/golang/mock v1.6.0 // indirect
	github.com/google/pprof v0.0.0-20230602150820-91b7bce49751 // indirect
	github.com/onsi/ginkgo/v2 v2.10.0 // indirect
	github.com/quic-go/qtls-go1-20 v0.3.0 // indirect
	github.com/stretchr/testify v1.8.3 // indirect
	golang.org/x/crypto v0.10.0 // indirect
	golang.org/x/mod v0.10.0 // indirect
	golang.org/x/net v0.11.0 // indirect
	golang.org/x/sys v0.9.0 // indirect
	golang.org/x/tools v0.9.3 // indirect
	google.golang.org/protobuf v1.30.0 // indirect
)

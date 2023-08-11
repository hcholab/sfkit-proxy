package quic

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
)

const Proto = "sfkit"

// Setup a bare-bones TLS config for the server
// https://github.com/quic-go/quic-go/blob/master/example/echo/echo.go
func generateTLSConfig() (tlsConf *tls.Config, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return
	}
	tlsConf = &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{Proto},
	}
	return
}

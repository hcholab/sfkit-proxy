package ice

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

const (
	Proto         = "sfkit"
	CertValidity  = 24 * time.Hour
	CertKeyLength = 2048
)

// Setup a bare-bones TLS cert for mTLS with a peer
// https://github.com/quic-go/quic-go/blob/master/example/echo/echo.go
func generateTLSCert(ipAddr net.IP) (tlsCert tls.Certificate, pemCert string, err error) {
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		IPAddresses:  []net.IP{ipAddr},
		NotAfter:     time.Now().Add(CertValidity),
	}
	key, err := rsa.GenerateKey(rand.Reader, CertKeyLength)
	if err != nil {
		return
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	pemCert = string(certPEM)

	tlsCert, err = tls.X509KeyPair(certPEM, keyPEM)
	return
}

func getTLSConfig(isClient bool, selfCert *tls.Certificate, peerCert *Certificate) (c *tls.Config, err error) {
	peerCertPool := x509.NewCertPool()
	if !peerCertPool.AppendCertsFromPEM([]byte(peerCert.PEM)) {
		err = fmt.Errorf("failed to parse peer certificate from: %s", peerCert.PEM)
		return
	}
	c = &tls.Config{
		Certificates: []tls.Certificate{*selfCert},
		NextProtos:   []string{Proto},
	}
	if isClient {
		c.RootCAs = peerCertPool
	} else {
		c.ClientAuth = tls.RequireAndVerifyClientCert
		c.ClientCAs = peerCertPool
	}
	return
}

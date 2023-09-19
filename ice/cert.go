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

const Proto = "sfkit"

// Setup a bare-bones TLS cert for mTLS with a peer
// https://github.com/quic-go/quic-go/blob/master/example/echo/echo.go
func generateTLSCert(ipAddr net.IP) (_ tls.Certificate, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		IPAddresses:  []net.IP{ipAddr},
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	return tls.X509KeyPair(certPEM, keyPEM)
}

func getTLSConfig(isClient bool, selfCert *tls.Certificate, peerCert []byte) (c *tls.Config, err error) {
	peerCertPool := x509.NewCertPool()
	if !peerCertPool.AppendCertsFromPEM(peerCert) {
		err = fmt.Errorf("failed to parse peer certificate from: %s", string(peerCert))
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

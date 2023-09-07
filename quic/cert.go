package quic

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

// Setup a bare-bones TLS config for the server
// https://github.com/quic-go/quic-go/blob/master/example/echo/echo.go
func generateTLSConfig(addr net.Addr) (tlsConf *tls.Config, err error) {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		err = fmt.Errorf("not a UDPAddr: %s", addr)
		return
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		IPAddresses:  []net.IP{udpAddr.IP},
		NotAfter:     time.Now().Add(time.Hour),
	}
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
		Certificates:       []tls.Certificate{tlsCert},
		NextProtos:         []string{Proto},
		InsecureSkipVerify: true, // TODO: implement certificate verificaiton
	}
	return
}

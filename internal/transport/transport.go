package transport

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

func TLSConfigFromSyncKey(syncKey string) (*tls.Config, error) {
	cert, err := randomSelfSigned()
	if err != nil {
		return nil, err
	}
	_ = syncKey // v1: channel encryption; sync_key validated in HELLO
	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true,
	}, nil
}

func randomSelfSigned() (tls.Certificate, error) {
	priv, err := newECDSAKey()
	if err != nil {
		return tls.Certificate{}, err
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "sps"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, priv.Public(), priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return tls.X509KeyPair(certPEM, keyPEM)
}

type Listener struct {
	ln net.Listener
}

func Listen(addr string, syncKey string) (*Listener, error) {
	cfg, err := TLSConfigFromSyncKey(syncKey)
	if err != nil {
		return nil, err
	}
	ln, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	return &Listener{ln: ln}, nil
}

func (l *Listener) Accept() (net.Conn, error) {
	conn, err := l.ln.Accept()
	if err != nil {
		return nil, err
	}
	EnableKeepAlive(conn)
	return conn, nil
}

func (l *Listener) Addr() net.Addr {
	return l.ln.Addr()
}

func (l *Listener) Close() error {
	return l.ln.Close()
}

func Dial(addr string, syncKey string, timeout time.Duration) (net.Conn, error) {
	cfg, err := TLSConfigFromSyncKey(syncKey)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: timeout, KeepAlive: 15 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	EnableKeepAlive(conn)
	return conn, nil
}

// EnableKeepAlive turns on TCP keepalives so NAT/WARP idle drops are detected.
func EnableKeepAlive(conn net.Conn) {
	c := conn
	if tc, ok := conn.(*tls.Conn); ok {
		c = tc.NetConn()
	}
	tcp, ok := c.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tcp.SetKeepAlive(true)
	_ = tcp.SetKeepAlivePeriod(15 * time.Second)
}

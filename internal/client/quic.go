package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"time"

	"github.com/pion/ice/v4"
	"github.com/quic-go/quic-go"
)

// quicConfig is shared by both DialQUIC and ListenQUIC.
// MaxIdleTimeout governs how quickly a dead peer is detected: quic-go closes the
// connection if no packets are received for this duration. 10s is fast enough that
// a killed receiver unblocks the sender's re-wait loop within ~10s, while still
// tolerating brief network hiccups. Active transfers are never idle so this timer
// only fires when the peer process is truly dead.
var quicConfig = &quic.Config{
	MaxIdleTimeout: 10 * time.Second,
}

// icePacketConn adapts *ice.Conn (net.Conn) to net.PacketConn so quic-go can use it.
// quic-go requires net.PacketConn; ICE provides a reliable stream-oriented conn over UDP.
// WriteTo ignores the destination address — ICE handles all routing internally.
type icePacketConn struct {
	*ice.Conn
}

func (p *icePacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, err := p.Conn.Read(b)
	return n, p.Conn.RemoteAddr(), err
}

func (p *icePacketConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	return p.Conn.Write(b)
}

// SetReadDeadline and SetWriteDeadline proxy to SetDeadline since ice.Conn
// exposes a single combined deadline.
func (p *icePacketConn) SetReadDeadline(t time.Time) error {
	return p.Conn.SetDeadline(t)
}

func (p *icePacketConn) SetWriteDeadline(t time.Time) error {
	return p.Conn.SetDeadline(t)
}

const quicNextProto = "wani"
const quicVerifyLabel = "wani-quic-verify"

// IsPeerClosedError reports whether err is the normal application-level close
// sent by the remote peer when it finishes (CloseWithError(0, "done")).
// This distinguishes an expected peer disconnect from a real transfer failure.
func IsPeerClosedError(err error) bool {
	if err == nil {
		return false
	}
	var appErr *quic.ApplicationError
	if errors.As(err, &appErr) {
		return appErr.ErrorCode == 0
	}
	var idleErr *quic.IdleTimeoutError
	if errors.As(err, &idleErr) {
		return true
	}
	return false
}

// quicHMAC returns HMAC-SHA256(key, quicVerifyLabel).
func quicHMAC(key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(quicVerifyLabel))
	return mac.Sum(nil)
}

// DialQUIC establishes a QUIC connection as the controlling (sender) peer over iceConn.
// It opens the first bidirectional stream and exchanges HMAC identity proofs before returning.
func DialQUIC(ctx context.Context, iceConn *ice.Conn, sharedKey []byte) (*quic.Conn, error) {
	pktConn := &icePacketConn{iceConn}
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // auth is via HMAC proof over SPAKE2-derived key
		NextProtos:         []string{quicNextProto},
	}
	conn, err := quic.Dial(ctx, pktConn, iceConn.RemoteAddr(), tlsCfg, quicConfig)
	if err != nil {
		return nil, fmt.Errorf("quic.DialQUIC: Dial: %w", err)
	}

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		conn.CloseWithError(1, "stream open failed")
		return nil, fmt.Errorf("quic.DialQUIC: OpenStream: %w", err)
	}
	defer stream.Close()

	proof := quicHMAC(sharedKey)
	if _, err := stream.Write(proof); err != nil {
		conn.CloseWithError(1, "write proof failed")
		return nil, fmt.Errorf("quic.DialQUIC: write proof: %w", err)
	}

	peerProof := make([]byte, len(proof))
	if _, err := io.ReadFull(stream, peerProof); err != nil {
		conn.CloseWithError(1, "read proof failed")
		return nil, fmt.Errorf("quic.DialQUIC: read proof: %w", err)
	}
	if !hmac.Equal(peerProof, proof) {
		conn.CloseWithError(1, "identity check failed")
		return nil, fmt.Errorf("quic.DialQUIC: identity check failed — wrong pairing code or tampered connection")
	}

	return conn, nil
}

// ListenQUIC establishes a QUIC connection as the controlled (receiver) peer over iceConn.
// It generates an ephemeral self-signed certificate (TLS auth is handled via HMAC proof),
// accepts the first QUIC connection, and exchanges HMAC identity proofs before returning.
func ListenQUIC(ctx context.Context, iceConn *ice.Conn, sharedKey []byte) (*quic.Conn, error) {
	cert, err := generateEphemeralCert()
	if err != nil {
		return nil, fmt.Errorf("quic.ListenQUIC: cert: %w", err)
	}

	pktConn := &icePacketConn{iceConn}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{quicNextProto},
	}
	listener, err := quic.Listen(pktConn, tlsCfg, quicConfig)
	if err != nil {
		return nil, fmt.Errorf("quic.ListenQUIC: Listen: %w", err)
	}
	defer listener.Close()

	conn, err := listener.Accept(ctx)
	if err != nil {
		return nil, fmt.Errorf("quic.ListenQUIC: Accept: %w", err)
	}

	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		conn.CloseWithError(1, "stream accept failed")
		return nil, fmt.Errorf("quic.ListenQUIC: AcceptStream: %w", err)
	}
	defer stream.Close()

	proof := quicHMAC(sharedKey)
	peerProof := make([]byte, len(proof))
	if _, err := io.ReadFull(stream, peerProof); err != nil {
		conn.CloseWithError(1, "read proof failed")
		return nil, fmt.Errorf("quic.ListenQUIC: read proof: %w", err)
	}
	if !hmac.Equal(peerProof, proof) {
		conn.CloseWithError(1, "identity check failed")
		return nil, fmt.Errorf("quic.ListenQUIC: identity check failed — wrong pairing code or tampered connection")
	}
	if _, err := stream.Write(proof); err != nil {
		conn.CloseWithError(1, "write proof failed")
		return nil, fmt.Errorf("quic.ListenQUIC: write proof: %w", err)
	}

	return conn, nil
}

// generateEphemeralCert generates a self-signed P-256 ECDSA certificate valid for 24 hours.
// TLS certificate validation is intentionally skipped by the dialer; identity is proven
// via HMAC over the SPAKE2-derived shared key.
func generateEphemeralCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "wani-ephemeral"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create cert: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}

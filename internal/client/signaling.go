package client

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"

	"github.com/gorilla/websocket"

	"github.com/Allergic2Bulletz/wani/internal/identity"
	"github.com/Allergic2Bulletz/wani/internal/protocol"
)

// SignalingClient manages a WebSocket connection to the wani signaling server.
// It has zero terminal I/O — all results are returned as values or errors.
type SignalingClient struct {
	conn *websocket.Conn
}

// Connect dials the wani signaling server at serverURL (e.g. "ws://host:8080/ws").
// The caller is responsible for calling Close when done.
func Connect(ctx context.Context, serverURL string) (*SignalingClient, error) {
	dialer := websocket.Dialer{}
	conn, _, err := dialer.DialContext(ctx, serverURL, nil)
	if err != nil {
		return nil, fmt.Errorf("signaling.Connect: %w", err)
	}
	return &SignalingClient{conn: conn}, nil
}

// Close closes the underlying WebSocket connection.
func (sc *SignalingClient) Close() error {
	return sc.conn.Close()
}

// CreateSession asks the server to create a new session.
// Returns the 4-word pairing code assigned by the server.
func (sc *SignalingClient) CreateSession() (string, error) {
	if err := sc.conn.WriteJSON(protocol.SignalMessage{Type: protocol.MsgCreateSession}); err != nil {
		return "", fmt.Errorf("signaling.CreateSession: send: %w", err)
	}
	msg, err := sc.readUntil(protocol.MsgSessionCreated)
	if err != nil {
		return "", fmt.Errorf("signaling.CreateSession: %w", err)
	}
	if msg.Code == "" {
		return "", fmt.Errorf("signaling.CreateSession: server returned empty code")
	}
	return msg.Code, nil
}

// JoinSession asks the server to join an existing session identified by code.
// Blocks until the server confirms both peers are connected (session_ready).
func (sc *SignalingClient) JoinSession(code string) error {
	if err := sc.conn.WriteJSON(protocol.SignalMessage{
		Type: protocol.MsgJoinSession,
		Code: code,
	}); err != nil {
		return fmt.Errorf("signaling.JoinSession: send: %w", err)
	}
	if _, err := sc.readUntil(protocol.MsgSessionReady); err != nil {
		return fmt.Errorf("signaling.JoinSession: %w", err)
	}
	return nil
}

// WaitForPeer blocks until the server notifies this client that the peer has
// joined (session_ready). Called by the sender after CreateSession.
func (sc *SignalingClient) WaitForPeer() error {
	if _, err := sc.readUntil(protocol.MsgSessionReady); err != nil {
		return fmt.Errorf("signaling.WaitForPeer: %w", err)
	}
	return nil
}

// ExchangeSPAKE2 runs the full SPAKE2 handshake for id over the relay channel.
// The send/recv closures are wired to relay messages on this connection.
// After ExchangeSPAKE2 returns nil, id.SharedSecret() is available.
func (sc *SignalingClient) ExchangeSPAKE2(id *identity.EphemeralIdentity) error {
	send := func(payload []byte) error {
		return sc.conn.WriteJSON(protocol.SignalMessage{
			Type:    protocol.MsgRelay,
			Payload: payload,
		})
	}
	recv := func() ([]byte, error) {
		msg, err := sc.readUntil(protocol.MsgRelay)
		if err != nil {
			return nil, err
		}
		return msg.Payload, nil
	}
	if err := id.RunExchange(send, recv); err != nil {
		return fmt.Errorf("signaling.ExchangeSPAKE2: %w", err)
	}
	return nil
}

// PingPong is called by the sender after a successful SPAKE2 exchange.
// It sends an HMAC-SHA256(K, "wani-ping") and verifies the receiver's pong.
func (sc *SignalingClient) PingPong(id *identity.EphemeralIdentity) error {
	key := id.SharedSecret()
	ping := computeHMAC(key, "wani-ping")
	if err := sc.conn.WriteJSON(protocol.SignalMessage{
		Type:    protocol.MsgRelay,
		Payload: ping,
	}); err != nil {
		return fmt.Errorf("signaling.PingPong: send ping: %w", err)
	}
	msg, err := sc.readUntil(protocol.MsgRelay)
	if err != nil {
		return fmt.Errorf("signaling.PingPong: recv pong: %w", err)
	}
	expected := computeHMAC(key, "wani-pong")
	if !hmac.Equal(msg.Payload, expected) {
		return fmt.Errorf("signaling.PingPong: pong HMAC mismatch — pairing failed")
	}
	return nil
}

// WaitAndPong is called by the receiver after a successful SPAKE2 exchange.
// It verifies the sender's ping and responds with the matching pong HMAC.
func (sc *SignalingClient) WaitAndPong(id *identity.EphemeralIdentity) error {
	key := id.SharedSecret()
	msg, err := sc.readUntil(protocol.MsgRelay)
	if err != nil {
		return fmt.Errorf("signaling.WaitAndPong: recv ping: %w", err)
	}
	expected := computeHMAC(key, "wani-ping")
	if !hmac.Equal(msg.Payload, expected) {
		return fmt.Errorf("signaling.WaitAndPong: ping HMAC mismatch — pairing failed")
	}
	pong := computeHMAC(key, "wani-pong")
	if err := sc.conn.WriteJSON(protocol.SignalMessage{
		Type:    protocol.MsgRelay,
		Payload: pong,
	}); err != nil {
		return fmt.Errorf("signaling.WaitAndPong: send pong: %w", err)
	}
	return nil
}

// readUntil reads messages from the server until one with the expected type
// arrives. Returns an error if the server sends an error message.
func (sc *SignalingClient) readUntil(wantType string) (protocol.SignalMessage, error) {
	for {
		var msg protocol.SignalMessage
		if err := sc.conn.ReadJSON(&msg); err != nil {
			return protocol.SignalMessage{}, fmt.Errorf("read: %w", err)
		}
		if msg.Type == protocol.MsgError {
			return protocol.SignalMessage{}, fmt.Errorf("server error: %s", msg.Message)
		}
		if msg.Type == wantType {
			return msg, nil
		}
		// Ignore unrecognised messages and keep reading.
	}
}

// computeHMAC returns HMAC-SHA256(key, label).
func computeHMAC(key []byte, label string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(label))
	return mac.Sum(nil)
}

// bytesEqual is a constant-time comparison used in tests; exported for clarity.
var bytesEqual = bytes.Equal

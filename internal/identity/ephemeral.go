package identity

import (
	"fmt"

	spake2go "github.com/backkem/spake2-go"
)

// Role identifies which side of the SPAKE2 exchange this peer plays.
type Role int

const (
	// RoleSender is SPAKE2 Party A (Client); initiates the exchange.
	RoleSender Role = iota
	// RoleReceiver is SPAKE2 Party B (Server); responds to the sender.
	RoleReceiver
)

// EphemeralIdentity derives a shared secret from a SPAKE2 exchange keyed on the
// pairing code. The secret is discarded after the transfer; nothing is persisted.
type EphemeralIdentity struct {
	role   Role
	spake  *spake2go.SPAKE2
	secret []byte
}

// NewEphemeralIdentity creates an identity for the given role keyed to password.
// password is typically the raw bytes of the pairing code string.
func NewEphemeralIdentity(role Role, password []byte) (*EphemeralIdentity, error) {
	var s *spake2go.SPAKE2
	switch role {
	case RoleSender:
		s = spake2go.NewClient(password, nil)
	case RoleReceiver:
		s = spake2go.NewServer(password, nil)
	default:
		return nil, fmt.Errorf("identity: unknown role %d", role)
	}
	return &EphemeralIdentity{role: role, spake: s}, nil
}

// RunExchange performs the full SPAKE2 handshake using the provided transport
// functions. Caller supplies send (write bytes to peer) and recv (read bytes
// from peer). After RunExchange returns nil, SharedSecret() is available.
//
// Wire flow – Sender (A):
//
//	msg  → send   (Start)
//	recv → serverMsg
//	confirm → send (Finish)
//	recv → serverConfirm
//	Verify → SharedKey
//
// Wire flow – Receiver (B):
//
//	recv → clientMsg
//	serverMsg → send  (Exchange)
//	recv → clientConfirm
//	serverConfirm → send (Confirm)
//	SharedKey
func (e *EphemeralIdentity) RunExchange(
	send func([]byte) error,
	recv func() ([]byte, error),
) error {
	switch e.role {
	case RoleSender:
		return e.runSender(send, recv)
	case RoleReceiver:
		return e.runReceiver(send, recv)
	default:
		return fmt.Errorf("identity.RunExchange: unknown role")
	}
}

func (e *EphemeralIdentity) runSender(send func([]byte) error, recv func() ([]byte, error)) error {
	msg, err := e.spake.Start()
	if err != nil {
		return fmt.Errorf("identity.RunExchange sender Start: %w", err)
	}
	if err := send(msg); err != nil {
		return fmt.Errorf("identity.RunExchange sender send Start: %w", err)
	}

	serverMsg, err := recv()
	if err != nil {
		return fmt.Errorf("identity.RunExchange sender recv Exchange: %w", err)
	}
	confirm, err := e.spake.Finish(serverMsg)
	if err != nil {
		return fmt.Errorf("identity.RunExchange sender Finish: %w", err)
	}
	if err := send(confirm); err != nil {
		return fmt.Errorf("identity.RunExchange sender send Finish: %w", err)
	}

	serverConfirm, err := recv()
	if err != nil {
		return fmt.Errorf("identity.RunExchange sender recv Confirm: %w", err)
	}
	if err := e.spake.Verify(serverConfirm); err != nil {
		return fmt.Errorf("identity.RunExchange sender Verify: %w", err)
	}

	key, err := e.spake.SharedKey()
	if err != nil {
		return fmt.Errorf("identity.RunExchange sender SharedKey: %w", err)
	}
	e.secret = key
	return nil
}

func (e *EphemeralIdentity) runReceiver(send func([]byte) error, recv func() ([]byte, error)) error {
	clientMsg, err := recv()
	if err != nil {
		return fmt.Errorf("identity.RunExchange receiver recv Start: %w", err)
	}
	serverMsg, err := e.spake.Exchange(clientMsg)
	if err != nil {
		return fmt.Errorf("identity.RunExchange receiver Exchange: %w", err)
	}
	if err := send(serverMsg); err != nil {
		return fmt.Errorf("identity.RunExchange receiver send Exchange: %w", err)
	}

	clientConfirm, err := recv()
	if err != nil {
		return fmt.Errorf("identity.RunExchange receiver recv Finish: %w", err)
	}
	serverConfirm, err := e.spake.Confirm(clientConfirm)
	if err != nil {
		return fmt.Errorf("identity.RunExchange receiver Confirm: %w", err)
	}
	if err := send(serverConfirm); err != nil {
		return fmt.Errorf("identity.RunExchange receiver send Confirm: %w", err)
	}

	key, err := e.spake.SharedKey()
	if err != nil {
		return fmt.Errorf("identity.RunExchange receiver SharedKey: %w", err)
	}
	e.secret = key
	return nil
}

// SharedSecret returns the derived shared secret. Panics if called before a
// successful RunExchange — callers must check RunExchange error first.
func (e *EphemeralIdentity) SharedSecret() []byte {
	if e.secret == nil {
		panic("identity: SharedSecret called before RunExchange completed")
	}
	return e.secret
}

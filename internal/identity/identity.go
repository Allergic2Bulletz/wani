package identity

// Identity represents a peer's identity for authentication.
type Identity interface {
	// SharedSecret returns the shared secret derived from key exchange.
	SharedSecret() []byte
}

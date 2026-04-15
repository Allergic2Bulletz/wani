package server

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const sessionTTL = 5 * time.Minute
const cleanupInterval = 1 * time.Minute

// reconnectTTL is how long a sender-only session persists after the receiver
// disconnects, giving the receiver a window to rejoin with the same code.
const reconnectTTL = 15 * time.Minute

// connSide identifies which side of a session a connection belongs to.
type connSide int

const (
	sideUnknown  connSide = iota
	sideSender            // created the session
	sideReceiver          // joined the session
)

// Session holds the two WebSocket connections for a paired transfer session.
// Each connection gets its own write mutex (gorilla requires single-writer per conn).
type Session struct {
	senderConn   *websocket.Conn
	senderMu     sync.Mutex
	receiverConn *websocket.Conn
	receiverMu   sync.Mutex
	expiresAt    time.Time
	// receiverGone is true when the receiver has disconnected but the session
	// is still alive so the receiver can rejoin with the same code.
	receiverGone bool
}

// writeToSender writes a JSON message to the sender connection under its mutex.
func (s *Session) writeToSender(v interface{}) error {
	s.senderMu.Lock()
	defer s.senderMu.Unlock()
	return s.senderConn.WriteJSON(v)
}

// writeToReceiver writes a JSON message to the receiver connection under its mutex.
func (s *Session) writeToReceiver(v interface{}) error {
	s.receiverMu.Lock()
	defer s.receiverMu.Unlock()
	return s.receiverConn.WriteJSON(v)
}

// SessionStore manages active signaling sessions indexed by pairing code.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	// connToCode maps a connection back to its pairing code for O(1) relay routing.
	connToCode map[*websocket.Conn]string
	// connToSide maps a connection to its role within the session.
	connToSide map[*websocket.Conn]connSide
}

// NewSessionStore creates an empty SessionStore.
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions:   make(map[string]*Session),
		connToCode: make(map[*websocket.Conn]string),
		connToSide: make(map[*websocket.Conn]connSide),
	}
}

// CreateSender stores a new session for the sender connection and returns the session.
func (ss *SessionStore) CreateSender(code string, conn *websocket.Conn) *Session {
	s := &Session{
		senderConn: conn,
		expiresAt:  time.Now().Add(sessionTTL),
	}
	ss.mu.Lock()
	ss.sessions[code] = s
	ss.connToCode[conn] = code
	ss.connToSide[conn] = sideSender
	ss.mu.Unlock()
	return s
}

// AddReceiver attaches a receiver connection to an existing session.
// Returns (session, true) on success. Fails if the session does not exist,
// or if a receiver is already actively connected.
// A receiver that previously disconnected (receiverGone == true) may rejoin.
func (ss *SessionStore) AddReceiver(code string, conn *websocket.Conn) (*Session, bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[code]
	if !ok {
		return nil, false
	}
	// Reject if a live receiver is already connected.
	if s.receiverConn != nil && !s.receiverGone {
		return nil, false
	}
	// Remove old conn mappings if this is a rejoin.
	if s.receiverConn != nil {
		delete(ss.connToCode, s.receiverConn)
		delete(ss.connToSide, s.receiverConn)
	}
	s.receiverConn = conn
	s.receiverGone = false
	// Refresh the session TTL on rejoin.
	s.expiresAt = time.Now().Add(sessionTTL)
	ss.connToCode[conn] = code
	ss.connToSide[conn] = sideReceiver
	return s, true
}

// Lookup returns the session and role for a given connection.
func (ss *SessionStore) Lookup(conn *websocket.Conn) (code string, s *Session, side connSide, ok bool) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	code, ok = ss.connToCode[conn]
	if !ok {
		return "", nil, sideUnknown, false
	}
	s = ss.sessions[code]
	side = ss.connToSide[conn]
	return code, s, side, true
}

// DisconnectReceiver marks the receiver as gone and extends the session TTL
// to give the receiver a window to rejoin. The old connection is removed from
// the lookup maps but the session entry is kept alive.
func (ss *SessionStore) DisconnectReceiver(code string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[code]
	if !ok {
		return
	}
	if s.receiverConn != nil {
		delete(ss.connToCode, s.receiverConn)
		delete(ss.connToSide, s.receiverConn)
		s.receiverConn = nil
	}
	s.receiverGone = true
	s.expiresAt = time.Now().Add(reconnectTTL)
}

// Delete removes a session and all its connection mappings.
func (ss *SessionStore) Delete(code string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[code]
	if !ok {
		return
	}
	delete(ss.connToCode, s.senderConn)
	delete(ss.connToSide, s.senderConn)
	if s.receiverConn != nil {
		delete(ss.connToCode, s.receiverConn)
		delete(ss.connToSide, s.receiverConn)
	}
	delete(ss.sessions, code)
}

// StartCleanup launches a background goroutine that evicts expired sessions.
// It stops when ctx is cancelled.
func (ss *SessionStore) StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ss.evictExpired()
			}
		}
	}()
}

func (ss *SessionStore) evictExpired() {
	now := time.Now()
	ss.mu.Lock()
	var expired []string
	for code, s := range ss.sessions {
		if now.After(s.expiresAt) {
			expired = append(expired, code)
		}
	}
	ss.mu.Unlock()
	for _, code := range expired {
		ss.Delete(code)
	}
}

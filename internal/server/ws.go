package server

import (
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/Allergic2Bulletz/wani/internal/protocol"
)

var upgrader = websocket.Upgrader{
	// Allow all origins for MVP — tighten for production.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleWS upgrades an HTTP connection to WebSocket and processes signaling messages.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an HTTP error response; just return.
		return
	}
	defer func() {
		conn.Close()
		s.cleanupConn(conn)
	}()

	for {
		var msg protocol.SignalMessage
		if err := conn.ReadJSON(&msg); err != nil {
			// Client disconnected or sent invalid JSON — clean up.
			return
		}
		if err := s.dispatch(conn, msg); err != nil {
			// Send error back to client then continue reading.
			writeErr(conn, err.Error())
		}
	}
}

// dispatch routes a received message to the appropriate handler.
func (s *Server) dispatch(conn *websocket.Conn, msg protocol.SignalMessage) error {
	switch msg.Type {
	case protocol.MsgCreateSession:
		return s.handleCreateSession(conn)
	case protocol.MsgJoinSession:
		return s.handleJoinSession(conn, msg.Code)
	case protocol.MsgRelay:
		return s.handleRelay(conn, msg.Payload)
	default:
		return fmt.Errorf("unknown message type: %q", msg.Type)
	}
}

// handleCreateSession generates a pairing code, stores the sender, and sends session_created.
func (s *Server) handleCreateSession(conn *websocket.Conn) error {
	code, err := protocol.GenerateCode(4)
	if err != nil {
		return fmt.Errorf("server: generate code: %w", err)
	}
	s.store.CreateSender(code, conn)
	return conn.WriteJSON(protocol.SignalMessage{
		Type: protocol.MsgSessionCreated,
		Code: code,
	})
}

// handleJoinSession attaches the receiver to an existing session and notifies both peers.
func (s *Server) handleJoinSession(conn *websocket.Conn, code string) error {
	if code == "" {
		return fmt.Errorf("join_session: missing code")
	}
	if !protocol.ValidateCode(code) {
		return fmt.Errorf("join_session: invalid code format")
	}
	session, ok := s.store.AddReceiver(code, conn)
	if !ok {
		return fmt.Errorf("join_session: session not found or already joined")
	}
	// Notify receiver first, then sender.
	if err := session.writeToReceiver(protocol.SignalMessage{Type: protocol.MsgSessionReady}); err != nil {
		return fmt.Errorf("join_session: notify receiver: %w", err)
	}
	if err := session.writeToSender(protocol.SignalMessage{Type: protocol.MsgSessionReady}); err != nil {
		return fmt.Errorf("join_session: notify sender: %w", err)
	}
	return nil
}

// handleRelay forwards the payload to the other peer in the session.
func (s *Server) handleRelay(conn *websocket.Conn, payload []byte) error {
	_, session, side, ok := s.store.Lookup(conn)
	if !ok {
		return fmt.Errorf("relay: connection not associated with any session")
	}
	msg := protocol.SignalMessage{Type: protocol.MsgRelay, Payload: payload}
	switch side {
	case sideSender:
		if session.receiverConn == nil {
			return fmt.Errorf("relay: receiver not yet connected")
		}
		return session.writeToReceiver(msg)
	case sideReceiver:
		return session.writeToSender(msg)
	default:
		return fmt.Errorf("relay: unknown side")
	}
}

// cleanupConn removes a connection and its session from the store on disconnect.
func (s *Server) cleanupConn(conn *websocket.Conn) {
	code, _, _, ok := s.store.Lookup(conn)
	if !ok {
		return
	}
	s.store.Delete(code)
}

// writeErr sends an error message to a single connection (best-effort).
func writeErr(conn *websocket.Conn, msg string) {
	_ = conn.WriteJSON(protocol.SignalMessage{
		Type:    protocol.MsgError,
		Message: msg,
	})
}

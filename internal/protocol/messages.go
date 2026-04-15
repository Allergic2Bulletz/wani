package protocol

// Message type constants for client→server direction.
const (
	MsgCreateSession  = "create_session"
	MsgJoinSession    = "join_session"
	MsgRelay          = "relay"
	MsgICECandidate   = "ice_candidate"
	MsgICECredentials = "ice_credentials"
)

// Message type constants for server→client direction (some overlap with relay).
const (
	MsgSessionCreated = "session_created"
	MsgSessionReady   = "session_ready"
	MsgError          = "error"
)

// SignalMessage is the wire format for all WebSocket messages.
// Fields are omitted from JSON when empty so the struct serves all message types.
type SignalMessage struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Payload []byte `json:"payload,omitempty"` // base64-encoded by encoding/json
	Message string `json:"message,omitempty"` // human-readable error text
}

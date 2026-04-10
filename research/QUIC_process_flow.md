═══════════════════════════════════════════════════════════════════
 PHASE 1: SETUP
═══════════════════════════════════════════════════════════════════

Client A's user runs:  wani send ./photos

Client A:
  1. Scans files, builds manifest (file list, sizes)
  2. Generates random pairing code: "blue-hammer-ocean"
  3. Connects to signaling server via WebSocket
  4. Tells server: "Create a session, I'm the sender"
  5. Server creates session, associates it with Client A
  6. Displays to user: "Share this code: blue-hammer-ocean"

  (Client A has NOT contacted STUN yet — that comes later)

═══════════════════════════════════════════════════════════════════
 PHASE 2: JOIN
═══════════════════════════════════════════════════════════════════

Client A's user tells their friend: "the code is blue-hammer-ocean"
  (via text message, Discord, phone call, etc.)

Client B's user runs:  wani receive blue-hammer-ocean

Client B:
  1. Connects to signaling server via WebSocket
  2. Tells server: "I want to join with code blue-hammer-ocean"
  3. Server matches Client B to Client A's session

═══════════════════════════════════════════════════════════════════
 PHASE 3: PAKE KEY EXCHANGE
═══════════════════════════════════════════════════════════════════

This happens over the signaling server's WebSocket connection:

Client A                    Signaling Server               Client B
  |                               |                            |
  |  Both know the pairing code "blue-hammer-ocean"            |
  |  but the server does NOT                                   |
  |                               |                            |
  |-- PAKE msg1 (blob derived -->|                             |
  |   from code + random)        |-- forward blob ----------->|
  |                               |                            |
  |                               |<-- PAKE msg2 (blob) ------|
  |<-- forward blob --------------|                            |
  |                               |                            |
  | [A derives session key K]     |  [B derives session key K] |
  |                               |                            |
  | The server saw both blobs     |                            |
  | but WITHOUT the code, it      |                            |
  | CANNOT derive K               |                            |

Result: Both clients now share a strong session key K.
The signaling server doesn't have it.

═══════════════════════════════════════════════════════════════════
 PHASE 4: ICE NEGOTIATION
═══════════════════════════════════════════════════════════════════

NOW both clients contact STUN and gather ICE candidates:

Client A                    Signaling Server               Client B
  |                               |                            |
  |-- contacts STUN server -------|                            |
  |   learns: public IP = 73.45.12.99:61000                   |
  |   knows:  local IP = 192.168.1.5:54321                    |
  |                               |-- contacts STUN server --->|
  |                               |   learns: 98.22.7.44:50200|
  |                               |   knows: 10.0.0.8:12345   |
  |                               |                            |
  |-- "my candidates are:" ----->|                             |
  |   192.168.1.5:54321 (local)  |-- forward --------------->|
  |   73.45.12.99:61000 (public) |                            |
  |   turn.server:3478 (relay)   |                            |
  |                               |                            |
  |                               |<-- "my candidates are:" --|
  |<-- forward ------------------|   10.0.0.8:12345 (local)  |
  |                               |   98.22.7.44:50200 (pub)  |
  |                               |   turn.server:3478 (relay)|

  (These candidate messages can be encrypted with key K
   from the PAKE exchange for extra security)

═══════════════════════════════════════════════════════════════════
 PHASE 5: HOLE PUNCHING
═══════════════════════════════════════════════════════════════════

Both clients now try to reach each other directly via UDP:

Client A                                              Client B
  |                                                      |
  |-- UDP to 10.0.0.8:12345 (B's local) -- X dropped    |
  |-- UDP to 98.22.7.44:50200 (B's public) ---- ???     |
  |                                                      |
  |   X dropped -- UDP to 192.168.1.5:54321 (A's local) |
  |          ??? ---- UDP to 73.45.12.99:61000 (A's pub) |
  |                                                      |
  | Because both sent to each other's public IP          |
  | at ~the same time, both NATs created mappings.       |
  | Subsequent packets get through!                      |
  |                                                      |
  |<========== UDP path established ===================>|
  |                                                      |
  | If this fails (symmetric NAT), ICE falls back to:   |
  | Both connect to TURN relay (UDP relay)               |
  | If TURN fails (UDP blocked), fall back to TCP relay  |

═══════════════════════════════════════════════════════════════════
 PHASE 6: QUIC CONNECTION
═══════════════════════════════════════════════════════════════════

Over the UDP path (direct or TURN-relayed):

Client A                                              Client B
  |                                                      |
  |-- QUIC handshake (TLS 1.3 built in) --------------->|
  |<-- QUIC handshake response -------------------------|
  |                                                      |
  | Connection encrypted. Now verify identity:           |
  |                                                      |
  |-- proof-of-knowledge of PAKE key K ---------------->|
  |<-- proof-of-knowledge of PAKE key K ----------------|
  |                                                      |
  | Both sides confirmed: "I'm talking to someone who    |
  | knows the pairing code, not an impersonator"         |

═══════════════════════════════════════════════════════════════════
 PHASE 7: FILE TRANSFER
═══════════════════════════════════════════════════════════════════

Client A                                              Client B
  |                                                      |
  |== QUIC Stream 0: manifest (file list, sizes) =====>|
  |                                                      |
  |  B inspects manifest, confirms acceptance            |
  |                                                      |
  |== QUIC Stream 1: photos/img001.jpg ===============>|
  |== QUIC Stream 2: photos/img002.jpg ===============>|
  |== QUIC Stream 3: photos/img003.jpg ===============>|
  |   (parallel, multiplexed, reliable, encrypted)       |
  |                                                      |
  |<== "transfer complete, all hashes verified" ========|
  |                                                      |
  | Both disconnect. Session destroyed.                  |
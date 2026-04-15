package client

import (
	"context"
	"fmt"
	"sync"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v3"
)

// DefaultICESTUNServers are the STUN servers used to gather server-reflexive candidates.
var DefaultICESTUNServers = []*stun.URI{
	{Scheme: stun.SchemeTypeSTUN, Host: "stun.cloudflare.com", Port: 3478},
	{Scheme: stun.SchemeTypeSTUN, Host: "stun.l.google.com", Port: 19302},
}

// NewICEAgent creates a pion ICE agent configured for UDP4 host and server-reflexive candidates.
// If stunServers is nil, DefaultICESTUNServers is used.
func NewICEAgent(stunServers []*stun.URI) (*ice.Agent, error) {
	if stunServers == nil {
		stunServers = DefaultICESTUNServers
	}
	agent, err := ice.NewAgentWithOptions(
		ice.WithUrls(stunServers),
		ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4}),
		ice.WithCandidateTypes([]ice.CandidateType{
			ice.CandidateTypeHost,
			ice.CandidateTypeServerReflexive,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("ice.NewICEAgent: %w", err)
	}
	return agent, nil
}

// GatherAndConnect performs the full ICE exchange over the signaling channel and returns
// an established *ice.Conn. isControlling should be true for the sender (QUIC dialer)
// and false for the receiver (QUIC listener).
//
// The exchange is gather-then-trickle: both peers finish local gathering before
// sending, which keeps the signaling reads sequential and avoids write contention.
//
// Wire order on signaling:
//  1. SendICECredentials (our ufrag/pwd)
//  2. SendICECandidate × N (marshalled candidate strings)
//  3. SendICECandidate nil (gathering-complete sentinel)
//  4. ReadICECredentials (peer's ufrag/pwd)
//  5. ReadICECandidate × N until nil sentinel
//  6. agent.Dial / agent.Accept
func GatherAndConnect(ctx context.Context, agent *ice.Agent, sc *SignalingClient, isControlling bool, logf func(string, ...any)) (*ice.Conn, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	// Collect candidates as they arrive; close gatherDone when gathering finishes.
	gatherDone := make(chan struct{})
	var mu sync.Mutex
	var localCandidates []ice.Candidate

	if err := agent.OnCandidate(func(c ice.Candidate) {
		if c == nil {
			close(gatherDone)
			return
		}
		mu.Lock()
		localCandidates = append(localCandidates, c)
		mu.Unlock()
	}); err != nil {
		return nil, fmt.Errorf("ice.GatherAndConnect: OnCandidate: %w", err)
	}

	if err := agent.GatherCandidates(); err != nil {
		return nil, fmt.Errorf("ice.GatherAndConnect: GatherCandidates: %w", err)
	}

	select {
	case <-gatherDone:
	case <-ctx.Done():
		return nil, fmt.Errorf("ice.GatherAndConnect: gathering: %w", ctx.Err())
	}

	// Send our credentials before candidates so the peer's ReadICECredentials
	// always sees credentials as the first ice message.
	ufrag, pwd, err := agent.GetLocalUserCredentials()
	if err != nil {
		return nil, fmt.Errorf("ice.GatherAndConnect: GetLocalUserCredentials: %w", err)
	}
	if err := sc.SendICECredentials(ufrag, pwd); err != nil {
		return nil, fmt.Errorf("ice.GatherAndConnect: %w", err)
	}

	mu.Lock()
	candidates := make([]ice.Candidate, len(localCandidates))
	copy(candidates, localCandidates)
	mu.Unlock()

	for _, c := range candidates {
		logf("[ice] local  %s %s %s\n", c.Type(), c.NetworkType(), c.Address())
		if err := sc.SendICECandidate([]byte(c.Marshal())); err != nil {
			return nil, fmt.Errorf("ice.GatherAndConnect: SendICECandidate: %w", err)
		}
	}
	// Send nil sentinel to signal end-of-gathering.
	if err := sc.SendICECandidate(nil); err != nil {
		return nil, fmt.Errorf("ice.GatherAndConnect: send sentinel: %w", err)
	}

	// Read peer's credentials (arrives before their candidates by protocol).
	remoteUfrag, remotePwd, err := sc.ReadICECredentials()
	if err != nil {
		return nil, fmt.Errorf("ice.GatherAndConnect: %w", err)
	}

	// Read peer's candidates until nil sentinel.
	for {
		raw, err := sc.ReadICECandidate()
		if err != nil {
			return nil, fmt.Errorf("ice.GatherAndConnect: ReadICECandidate: %w", err)
		}
		if len(raw) == 0 {
			break // sentinel: peer's gathering complete
		}
		c, err := ice.UnmarshalCandidate(string(raw))
		if err != nil {
			return nil, fmt.Errorf("ice.GatherAndConnect: UnmarshalCandidate: %w", err)
		}
		logf("[ice] remote %s %s %s\n", c.Type(), c.NetworkType(), c.Address())
		if err := agent.AddRemoteCandidate(c); err != nil {
			return nil, fmt.Errorf("ice.GatherAndConnect: AddRemoteCandidate: %w", err)
		}
	}

	if isControlling {
		conn, err := agent.Dial(ctx, remoteUfrag, remotePwd)
		if err != nil {
			return nil, fmt.Errorf("ice.GatherAndConnect: Dial: %w", err)
		}
		logSelectedPair(agent, logf)
		return conn, nil
	}
	conn, err := agent.Accept(ctx, remoteUfrag, remotePwd)
	if err != nil {
		return nil, fmt.Errorf("ice.GatherAndConnect: Accept: %w", err)
	}
	logSelectedPair(agent, logf)
	return conn, nil
}

// logSelectedPair prints the winning ICE candidate pair after connectivity is established.
func logSelectedPair(agent *ice.Agent, logf func(string, ...any)) {
	pair, err := agent.GetSelectedCandidatePair()
	if err != nil || pair == nil {
		return
	}
	logf("[ice] selected pair: local=%s(%s %s) remote=%s(%s %s)\n",
		pair.Local.Address(), pair.Local.Type(), pair.Local.NetworkType(),
		pair.Remote.Address(), pair.Remote.Type(), pair.Remote.NetworkType(),
	)
}

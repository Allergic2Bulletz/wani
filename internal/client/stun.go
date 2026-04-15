package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/pion/stun/v3"
)

// DefaultSTUNServers is the list of public STUN servers queried in order.
var DefaultSTUNServers = []string{
	"stun.cloudflare.com:3478",
	"stun.l.google.com:19302",
}

// perServerTimeout is the deadline for a single STUN query.
const perServerTimeout = 3 * time.Second

// DiscoverPublicAddr queries STUN servers to discover this host's
// server-reflexive address (public IP:port as seen by the internet).
// Tries each server in order; returns the first successful result.
// Returns an error if all servers fail.
func DiscoverPublicAddr(ctx context.Context) (net.UDPAddr, error) {
	var errs []error
	for _, server := range DefaultSTUNServers {
		serverCtx, cancel := context.WithTimeout(ctx, perServerTimeout)
		addr, err := querySTUN(serverCtx, server)
		cancel()
		if err == nil {
			return addr, nil
		}
		errs = append(errs, err)
	}
	return net.UDPAddr{}, fmt.Errorf("all STUN servers failed: %w", errors.Join(errs...))
}

func querySTUN(ctx context.Context, server string) (net.UDPAddr, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp4", server)
	if err != nil {
		return net.UDPAddr{}, fmt.Errorf("dial %s: %w", server, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}

	c, err := stun.NewClient(conn)
	if err != nil {
		return net.UDPAddr{}, fmt.Errorf("stun client %s: %w", server, err)
	}
	defer c.Close()

	msg := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	var (
		xorAddr stun.XORMappedAddress
		respErr error
	)
	if err := c.Do(msg, func(res stun.Event) {
		if res.Error != nil {
			respErr = res.Error
			return
		}
		respErr = xorAddr.GetFrom(res.Message)
	}); err != nil {
		return net.UDPAddr{}, fmt.Errorf("stun request %s: %w", server, err)
	}
	if respErr != nil {
		return net.UDPAddr{}, fmt.Errorf("stun response %s: %w", server, respErr)
	}
	return net.UDPAddr{IP: xorAddr.IP, Port: xorAddr.Port}, nil
}

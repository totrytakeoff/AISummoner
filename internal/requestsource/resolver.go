// Package requestsource derives one bounded-admission source key from an HTTP
// request without trusting caller-controlled forwarding headers.
package requestsource

import (
	"errors"
	"net/http"
	"net/netip"
)

const HeaderName = "X-AISummoner-Client-IP"

var ErrInvalidSource = errors.New("invalid request source")

// Resolver trusts the dedicated source header only from an explicitly listed
// immediate peer. It is immutable and safe for concurrent use.
type Resolver struct {
	trusted map[netip.Addr]struct{}
}

// New constructs a Resolver from already validated exact proxy addresses.
// The input is copied; an empty list selects direct-peer mode.
func New(trustedProxyIPs []netip.Addr) *Resolver {
	trusted := make(map[netip.Addr]struct{}, len(trustedProxyIPs))
	for _, address := range trustedProxyIPs {
		trusted[address.Unmap()] = struct{}{}
	}
	return &Resolver{trusted: trusted}
}

// Resolve returns one canonical literal IP. Untrusted peers always resolve to
// their immediate address and their proxy headers are deliberately ignored.
func (resolver *Resolver) Resolve(request *http.Request) (string, error) {
	if request == nil {
		return "", ErrInvalidSource
	}
	peer, err := parseRemoteAddress(request.RemoteAddr)
	if err != nil {
		return "", ErrInvalidSource
	}
	if resolver == nil {
		return peer.String(), nil
	}
	if _, trusted := resolver.trusted[peer]; !trusted {
		return peer.String(), nil
	}

	values := request.Header.Values(HeaderName)
	if len(values) != 1 {
		return "", ErrInvalidSource
	}
	client, err := parseCanonicalLiteral(values[0])
	if err != nil {
		return "", ErrInvalidSource
	}
	return client.String(), nil
}

func parseRemoteAddress(value string) (netip.Addr, error) {
	addressPort, err := netip.ParseAddrPort(value)
	if err != nil {
		return netip.Addr{}, ErrInvalidSource
	}
	address := addressPort.Addr()
	if address.Zone() != "" {
		return netip.Addr{}, ErrInvalidSource
	}
	address = address.Unmap()
	if !validUnicast(address) {
		return netip.Addr{}, ErrInvalidSource
	}
	return address, nil
}

func parseCanonicalLiteral(value string) (netip.Addr, error) {
	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, ErrInvalidSource
	}
	address = address.Unmap()
	if !validUnicast(address) || value != address.String() {
		return netip.Addr{}, ErrInvalidSource
	}
	return address, nil
}

func validUnicast(address netip.Addr) bool {
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() {
		return false
	}
	return !address.Is4() || address != netip.AddrFrom4([4]byte{255, 255, 255, 255})
}

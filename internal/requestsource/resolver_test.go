package requestsource

import (
	"errors"
	"net/http"
	"net/netip"
	"testing"
)

func TestDirectPeerIgnoresEveryProxyHeader(t *testing.T) {
	request := &http.Request{RemoteAddr: "[::ffff:192.0.2.10]:4321", Header: make(http.Header)}
	request.Header.Add(HeaderName, "not-an-address")
	request.Header.Add(HeaderName, "198.51.100.1, 198.51.100.2")
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	request.Header.Set("Forwarded", "for=203.0.113.10")

	source, err := New(nil).Resolve(request)
	if err != nil {
		t.Fatal(err)
	}
	if source != "192.0.2.10" {
		t.Fatalf("direct source = %q, want canonical immediate peer", source)
	}
}

func TestTrustedProxyRequiresOneCanonicalLiteralClientIP(t *testing.T) {
	resolver := New([]netip.Addr{netip.MustParseAddr("10.20.0.20")})
	request := &http.Request{RemoteAddr: "10.20.0.20:443", Header: make(http.Header)}
	request.Header.Set(HeaderName, "2001:db8::5")
	source, err := resolver.Resolve(request)
	if err != nil {
		t.Fatal(err)
	}
	if source != "2001:db8::5" {
		t.Fatalf("proxied source = %q", source)
	}

	invalid := []struct {
		name   string
		values []string
	}{
		{name: "missing"},
		{name: "repeated", values: []string{"192.0.2.1", "192.0.2.2"}},
		{name: "comma separated", values: []string{"192.0.2.1,192.0.2.2"}},
		{name: "leading whitespace", values: []string{" 192.0.2.1"}},
		{name: "trailing whitespace", values: []string{"192.0.2.1 "}},
		{name: "noncanonical IPv6", values: []string{"2001:0db8::5"}},
		{name: "mapped IPv4", values: []string{"::ffff:192.0.2.1"}},
		{name: "zone", values: []string{"fe80::1%eth0"}},
		{name: "unspecified", values: []string{"0.0.0.0"}},
		{name: "multicast", values: []string{"ff02::1"}},
		{name: "broadcast", values: []string{"255.255.255.255"}},
		{name: "hostname", values: []string{"client.example"}},
		{name: "port", values: []string{"192.0.2.1:80"}},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			candidate := &http.Request{RemoteAddr: request.RemoteAddr, Header: make(http.Header)}
			for _, value := range test.values {
				candidate.Header.Add(HeaderName, value)
			}
			if _, err := resolver.Resolve(candidate); !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("Resolve error = %v", err)
			}
		})
	}
}

func TestTrustedProxyMatchIsExactAndConstructorCopiesInput(t *testing.T) {
	trusted := []netip.Addr{netip.MustParseAddr("10.20.0.20")}
	resolver := New(trusted)
	trusted[0] = netip.MustParseAddr("10.20.0.21")

	trustedRequest := &http.Request{RemoteAddr: "10.20.0.20:1234", Header: make(http.Header)}
	trustedRequest.Header.Set(HeaderName, "192.0.2.50")
	if source, err := resolver.Resolve(trustedRequest); err != nil || source != "192.0.2.50" {
		t.Fatalf("trusted source=%q error=%v", source, err)
	}
	untrustedRequest := &http.Request{RemoteAddr: "10.20.0.21:1234", Header: make(http.Header)}
	untrustedRequest.Header.Set(HeaderName, "192.0.2.51")
	if source, err := resolver.Resolve(untrustedRequest); err != nil || source != "10.20.0.21" {
		t.Fatalf("untrusted source=%q error=%v", source, err)
	}
}

func TestMalformedImmediatePeerFailsWithFixedError(t *testing.T) {
	resolver := New(nil)
	for _, remote := range []string{"", "proxy.example:80", "192.0.2.1:not-a-port", "0.0.0.0:80", "[ff02::1]:80", "[fe80::1%eth0]:80"} {
		request := &http.Request{RemoteAddr: remote, Header: make(http.Header)}
		_, err := resolver.Resolve(request)
		if !errors.Is(err, ErrInvalidSource) || err.Error() != "invalid request source" {
			t.Fatalf("remote %q error = %v", remote, err)
		}
	}
}

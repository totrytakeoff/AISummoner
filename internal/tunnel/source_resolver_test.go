package tunnel

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/requestsource"
)

func TestTrustedProxyTunnelLimitSeparatesClientSources(t *testing.T) {
	gateway := newLifecycleGateway(t, NewManager(), 10*time.Millisecond, time.Second, GatewayOptions{
		PreAuthPerMinute: 2,
		SourceResolver:   requestsource.New([]netip.Addr{netip.MustParseAddr("10.0.0.2")}),
	})
	t.Cleanup(gateway.Close)
	const (
		proxyAddress = "10.0.0.2:8443"
		sourceA      = "198.51.100.41"
		sourceB      = "198.51.100.42"
	)

	for attempt := 0; attempt < 2; attempt++ {
		response := serveTunnelAttempt(gateway, proxyAddress, sourceHeaders(sourceA))
		if response.Code == http.StatusTooManyRequests {
			t.Fatalf("source A attempt %d was rejected early: body=%s", attempt+1, response.Body.String())
		}
	}
	if response := serveTunnelAttempt(gateway, proxyAddress, sourceHeaders(sourceA)); response.Code != http.StatusTooManyRequests {
		t.Fatalf("source A limit status=%d body=%s", response.Code, response.Body.String())
	}
	if response := serveTunnelAttempt(gateway, proxyAddress, sourceHeaders(sourceB)); response.Code == http.StatusTooManyRequests {
		t.Fatalf("source B was collapsed into source A: body=%s", response.Body.String())
	}

	gateway.limiter.mu.Lock()
	entryA := gateway.limiter.entries[sourceA]
	entryB := gateway.limiter.entries[sourceB]
	entryCount := len(gateway.limiter.entries)
	gateway.limiter.mu.Unlock()
	if entryCount != 2 || entryA.count != 2 || entryB.count != 1 {
		t.Fatalf("trusted source windows collapsed: count=%d A=%#v B=%#v", entryCount, entryA, entryB)
	}
}

func TestUntrustedTunnelPeerCannotForgeLimiterSource(t *testing.T) {
	gateway := newLifecycleGateway(t, NewManager(), 10*time.Millisecond, time.Second, GatewayOptions{
		PreAuthPerMinute: 2,
		SourceResolver:   requestsource.New([]netip.Addr{netip.MustParseAddr("10.0.0.2")}),
	})
	t.Cleanup(gateway.Close)
	const directSource = "203.0.113.51"

	for attempt := 0; attempt < 3; attempt++ {
		headers := sourceHeaders(fmt.Sprintf("198.51.100.%d", attempt+1))
		headers.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", attempt+1))
		headers.Set("Forwarded", fmt.Sprintf("for=192.0.2.%d", attempt+1))
		response := serveTunnelAttempt(gateway, directSource+":9443", headers)
		if attempt < 2 && response.Code == http.StatusTooManyRequests {
			t.Fatalf("untrusted attempt %d was rejected early: body=%s", attempt+1, response.Body.String())
		}
		if attempt == 2 && response.Code != http.StatusTooManyRequests {
			t.Fatalf("forged headers changed key: status=%d body=%s", response.Code, response.Body.String())
		}
	}

	gateway.limiter.mu.Lock()
	entry := gateway.limiter.entries[directSource]
	entryCount := len(gateway.limiter.entries)
	gateway.limiter.mu.Unlock()
	if entryCount != 1 || entry.count != 2 {
		t.Fatalf("untrusted source state=%d direct=%#v", entryCount, entry)
	}
}

func TestMalformedTrustedTunnelSourceFailsBeforeAdmissionWithoutLogging(t *testing.T) {
	gateway := newLifecycleGateway(t, NewManager(), 10*time.Millisecond, time.Second, GatewayOptions{
		PreAuthPerMinute: 2,
		SourceResolver:   requestsource.New([]netip.Addr{netip.MustParseAddr("10.0.0.2")}),
	})
	t.Cleanup(gateway.Close)
	var logs bytes.Buffer
	gateway.logger = slog.New(slog.NewTextHandler(&logs, nil))

	cases := []struct {
		name    string
		headers http.Header
	}{
		{name: "missing", headers: make(http.Header)},
		{name: "repeated", headers: http.Header{requestsource.HeaderName: {"198.51.100.61", "198.51.100.62"}}},
		{name: "comma separated", headers: http.Header{requestsource.HeaderName: {"198.51.100.61,198.51.100.62"}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := serveTunnelAttempt(gateway, "10.0.0.2:8080", testCase.headers)
			if response.Code != http.StatusBadRequest || response.Body.String() != "invalid request\n" {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
	gateway.limiter.mu.Lock()
	limiterEntries := len(gateway.limiter.entries)
	gateway.limiter.mu.Unlock()
	if limiterEntries != 0 {
		t.Fatalf("malformed source mutated limiter: entries=%d", limiterEntries)
	}
	if got := len(gateway.preAuth); got != 0 {
		t.Fatalf("malformed source consumed pre-auth slot: slots=%d", got)
	}
	for _, forbidden := range []string{
		requestsource.HeaderName, "198.51.100.61", requestsource.ErrInvalidSource.Error(),
	} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("source failure log exposed %q: %s", forbidden, logs.String())
		}
	}
}

func TestTunnelLogsNeverIncludeResolvedSource(t *testing.T) {
	const sourceSentinel = requestsource.HeaderName + "=198.51.100.71"
	manager := NewManager()
	gateway := newLifecycleGateway(t, manager, 10*time.Millisecond, time.Second, GatewayOptions{
		SourceResolver: fixedSourceResolver{source: sourceSentinel},
	})
	var logs bytes.Buffer
	gateway.logger = slog.New(slog.NewTextHandler(&logs, nil))
	server := httptest.NewServer(gateway)
	defer server.Close()
	defer gateway.Close()

	// A rejected proof exercises the protocol-error log. A complete proof
	// exercises the authenticated log. Joining both handlers before reading the
	// buffer keeps the test deterministic and race-safe.
	rejectManualPeer(t, server.URL, testIdentity(t))
	peer := authenticateManualPeer(t, server.URL, testIdentity(t))
	peer.Close()
	gateway.Close()

	contents := logs.String()
	for _, required := range []string{"device tunnel closed", "device tunnel authenticated"} {
		if !strings.Contains(contents, required) {
			t.Fatalf("expected log path %q was not exercised: %s", required, contents)
		}
	}
	for _, forbidden := range []string{sourceSentinel, requestsource.HeaderName, "198.51.100.71"} {
		if strings.Contains(contents, forbidden) {
			t.Fatalf("tunnel log exposed resolved source %q: %s", forbidden, contents)
		}
	}
}

type fixedSourceResolver struct {
	source string
	err    error
}

func (resolver fixedSourceResolver) Resolve(*http.Request) (string, error) {
	if resolver.err != nil {
		return "", resolver.err
	}
	if resolver.source == "" {
		return "", errors.New("fixed source is empty")
	}
	return resolver.source, nil
}

func sourceHeaders(source string) http.Header {
	headers := make(http.Header)
	headers.Set(requestsource.HeaderName, source)
	return headers
}

func serveTunnelAttempt(gateway *Gateway, remoteAddress string, headers http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "http://aisummoner.test/api/v1/tunnel", nil)
	request.RemoteAddr = remoteAddress
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	return response
}

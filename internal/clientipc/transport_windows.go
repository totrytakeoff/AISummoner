//go:build windows

package clientipc

import (
	"context"
	"net"

	"github.com/aisummoner/aisummoner/internal/winpipe"
)

type windowsTransport struct{}

func currentTransport() localTransport { return windowsTransport{} }

func (windowsTransport) ValidateEndpoint(endpoint string) error {
	return winpipe.ValidateName(endpoint)
}

func (transport windowsTransport) ValidateEndpointForDataDirectory(_ string, endpoint string) error {
	return transport.ValidateEndpoint(endpoint)
}

func (windowsTransport) DefaultEndpoint(string) string { return winpipe.DefaultName }

func (windowsTransport) Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	return winpipe.Dial(ctx, endpoint)
}

func (windowsTransport) Listen(endpoint string) (authenticatedListener, error) {
	return winpipe.Listen(endpoint)
}

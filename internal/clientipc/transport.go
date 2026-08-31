package clientipc

import (
	"context"
	"net"
)

// authenticatedListener authenticates a local peer against the exact accepted
// transport instance. Authenticate must run before protocol bytes are read.
type authenticatedListener interface {
	net.Listener
	Authenticate(net.Conn) error
}

// localTransport owns every OS-specific endpoint, listener and peer-security
// rule. Framing and dispatch remain independent of Unix sockets/named pipes.
type localTransport interface {
	ValidateEndpoint(string) error
	ValidateEndpointForDataDirectory(string, string) error
	DefaultEndpoint(string) string
	Dial(context.Context, string) (net.Conn, error)
	Listen(string) (authenticatedListener, error)
}

func ValidateEndpointForDataDirectory(dataDirectory, endpoint string) error {
	return currentTransport().ValidateEndpointForDataDirectory(dataDirectory, endpoint)
}

func DefaultEndpoint(dataDirectory string) string {
	return currentTransport().DefaultEndpoint(dataDirectory)
}

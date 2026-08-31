//go:build windows

package windowsprobe

import (
	"net"
	"sync/atomic"

	"github.com/aisummoner/aisummoner/internal/winpipe"
)

const DefaultQtPipeName = winpipe.DefaultName

func FullPipePath(qtServerName string) (string, error) {
	return winpipe.FullPath(qtServerName)
}

// ListenVerifiedPipe keeps the probe's accept-only-authenticated contract while
// delegating the carrier, DACL and exact peer check to the production helper.
func ListenVerifiedPipe(qtServerName string) (net.Listener, error) {
	listener, err := winpipe.Listen(qtServerName)
	if err != nil {
		return nil, err
	}
	return &verifiedPipeListener{Listener: listener, authenticate: listener.Authenticate}, nil
}

type verifiedPipeListener struct {
	net.Listener
	authenticate func(net.Conn) error
	rejected     atomic.Uint64
}

func (listener *verifiedPipeListener) Accept() (net.Conn, error) {
	for {
		connection, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := listener.authenticate(connection); err == nil {
			return connection, nil
		}
		listener.rejected.Add(1)
		_ = connection.Close()
	}
}

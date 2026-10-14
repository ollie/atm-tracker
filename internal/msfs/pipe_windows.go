package msfs

import (
	"net"

	"github.com/Microsoft/go-winio"
)

func DialPipe() (net.Conn, error) {
	timeout := dialTimeout
	return winio.DialPipe(PipeName, &timeout)
}

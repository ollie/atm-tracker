//go:build !windows

package msfs

import (
	"errors"
	"net"
)

func DialPipe() (net.Conn, error) {
	return nil, errors.New("only runs on Windows")
}

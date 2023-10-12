// go build +linux
package server

import (
	"context"
	"net"
	"syscall"

	"github.com/Joe-Degs/dit"
	"golang.org/x/sys/unix"
)

func udpListen(addr string) (conn *dit.Conn, err error) {
	config := &net.ListenConfig{
		Control: func(net, addr string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				// set socket option to let multiple processes to
				// listen on the same port
				unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, syscall.SO_REUSEADDR, 1)

				// set the priority of the socket high to recieve the
				// fucking packets becuase no packets are coming
				// socket priority [low - high] => [1 - 7]
				unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, syscall.SO_PRIORITY, 7)
			})
		},
	}

	if conn, err = dit.ListenConfigConn(context.Background(), config, addr); err != nil {
		return nil, err
	}
	return
}

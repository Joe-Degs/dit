//go:build darwin
// +build darwin

package server

import (
	"context"
	"net"
	"os"
	"syscall"

	"github.com/Joe-Degs/dit"
	"golang.org/x/sys/unix"
)

func udpListen(addr string) (conn *dit.Conn, err error) {
	config := &net.ListenConfig{
		Control: func(net, addr string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, syscall.SO_REUSEADDR, 1)

				// mac doesn't have SO_PRIORITY so we omit it over here
				// unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, syscall.SO_PRIORITY, 7)
			})
		},
	}

	if conn, err = dit.ListenConfigConn(context.Background(), config, addr); err != nil {
		return nil, err
	}
	return
}

func restartProcess() error {
	return syscall.Exec(os.Args[0], os.Args, os.Environ())
}

package server

import (
	"context"
	"log"
	"net"
	"syscall"

	"github.com/Joe-Degs/dit"
	"github.com/Joe-Degs/dit/internal/config"
	"golang.org/x/sys/unix"
)

type server struct {
	listener *dit.Conn
	opts     *config.Opts
}

// newServer returns a new tftp server
func newServer(opts *config.Opts) (*server, error) {
	config := &net.ListenConfig{
		Control: func(net, addr string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				// set socket option to let multiple processes to
				// listen on the same port
				unix.SetsockoptInt(int(fd), unix.SOL_SOCKET,
					syscall.SO_REUSEADDR, 1)

				// set the priority of the socket high to recieve the
				// fucking packets becuase no packets are coming
				// socket priority [low - high] => [1 - 7]
				unix.SetsockoptInt(int(fd), unix.SOL_SOCKET,
					syscall.SO_PRIORITY, 7)
			})
		},
	}

	listener, err := dit.ListenConfigConn(context.Background(), config,
		"udp", opts.Address)
	if err != nil {
		return nil, err
	}

	return &server{listener, opts}, nil
}

func (s *server) start() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			log.Fatal(err)
		}

		go s.handle(conn)
	}
}

func (s *server) handle(conn *dit.Conn) {
	req, bufferFunc := conn.RequestBuffer()

	log.Printf("[New Connection] (addr) %s (remote) %s (type) %s", conn.Addr(),
		conn.DestinationTID(), req.Opcode)

	buffer, err := bufferFunc(req.Filename)
	if err != nil {
		// TODO(Joe-Degs):
		// check all the possible causes of the error
		log.Fatal(err)
	}

	if req.Opcode == dit.Rrq {
		s.handleReadRequest(conn, buffer)
		return
	} else if req.Opcode == dit.Wrq {
		s.handleWriteRequest(conn, buffer)
		return
	}
}

func (s *server) handleReadRequest(conn *dit.Conn, buffer *dit.FileBuffer) {
	buf := make([]byte, 512)
	n, err := buffer.ReadNext(buf)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("[Handling Conn] (read) %d (tmp buffer len) %d", n,
		buffer.BufferLen())
}

func (s *server) handleWriteRequest(conn *dit.Conn, buffer *dit.FileBuffer) {
	return
}

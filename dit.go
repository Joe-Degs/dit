// dit implements the tftp protocol as specified in rfc 1350 at
// https://datatracker.ietf.org/doc/html/rfc1350
package dit

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

var (
	// Packet from an unexpected transfer identifier (address)
	ErrUnexpectedTID = errors.New("dit: packet from unexpected TID (host)")

	// Calling accept on tftp clients ( this is not allowed )
	ErrClientAccept = errors.New("dit: client cannot accept new connections")
)

// Conn is a tftp connection and provides functionality to send,
// recieve and serve files over the protocol
type Conn struct {
	// This is the primary connection, an active udp listener.
	// If the Conn is a client (connected=true) it accepts
	// packets from only destTID and writes only to it.
	// If the Conn is a listener/server (connected=false) it
	// can accept read/write requests from any addr and create a
	// new client connection to handle the request
	c *net.UDPConn

	// This holds the address that a client is actively connected to.
	destTID netip.AddrPort

	// True if the Conn is a client / actively reading/writing to another
	// client. False if Conn is a server and only handling read/write requests.
	connected bool

	// RequestBuffer returns the request and a file buffer func on connected
	// client, this is left as "nil" on listening connections and clients that
	// are not yet connected to any other remote clients for transfer
	RequestBuffer func() (*ReadWriteRequest, FileBufferFunc)

	mu sync.Mutex
}

// Dial connects to a TFTP server and returns a half open connection.
//
// use the `connect` method to fully open the connection
func Dial(network, address string) (*Conn, error) {
	if !strings.Contains(network, "udp") {
		return nil, fmt.Errorf("dit: protocol runs only over udp, %s", network)
	}

	// get an ephemeral local address for the client to listen for packets
	tid, err := net.ResolveUDPAddr(network, "localhost:0")
	if err != nil {
		return nil, err
	}

	// resolve server address and store it as the initial TID of the server
	raddr, err := net.ResolveUDPAddr(network, address)
	if err != nil {
		return nil, err
	}

	c, err := net.ListenUDP(network, tid)
	if err != nil {
		return nil, err
	}

	return &Conn{c: c, destTID: raddr.AddrPort()}, nil
}

// Listen announces on the network and returns a Conn that is capable of
// waiting for and handling read/write requests
//
// The Conn returned from Listen does not respond to any clients, it merely
// listens for read/write requests using the Accept method and returns
// a Conn that is capable of responding back to clients
func Listen(network, address string) (*Conn, error) {
	if !strings.Contains(network, "udp") {
		return nil, fmt.Errorf("dit: protocol runs only over udp, %s", network)
	}

	addr, err := net.ResolveUDPAddr(network, address)
	if err != nil {
		return nil, err
	}

	c, err := net.ListenUDP(network, addr)
	if err != nil {
		return nil, err
	}

	return newListenConn(c)
}

func newListenConn(conn net.PacketConn) (*Conn, error) {
	udpConn, ok := conn.(*net.UDPConn)
	if !ok {
		return nil, fmt.Errorf("dit: only works over udp protocol: %T", conn)
	}
	return &Conn{
		c: udpConn,
	}, nil
}

// ListenConfigConn gives you more control over the behaviour of the underlying
// socket.
//
// This makes it possible to do things like set platform specific socket options
// and adding a context to control lifetime of connections.
func ListenConfigConn(ctx context.Context, cfg *net.ListenConfig, network, address string) (*Conn, error) {
	if !strings.Contains(network, "udp") {
		return nil, fmt.Errorf("dit: protocol runs only over udp, %s", network)
	}
	conn, err := cfg.ListenPacket(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("dit: listenconfigconn: %w", err)
	}
	return newListenConn(conn)
}

func (c *Conn) Addr() net.Addr {
	return c.c.LocalAddr()
}

// Accept waits and returns a Conn capable of responding to read/write
// requests appropriately.
//
// This function is only supposed to be called on listening Conn's
func (c *Conn) Accept() (*Conn, error) {
	if c.connected {
		return nil, ErrClientAccept
	}

	// lock down connection so no other go routine can change
	// the connection state while it is actively accepting
	// connections
	c.mu.Lock()
	defer c.mu.Unlock()

	// TODO(Joe-Degs): this seems like a bad way to do this. go see the
	// go package see how they allocate memory for accept
	buf := make([]byte, 256)
	for {
		n, clientTID, err := c.c.ReadFromUDP(buf)
		if err != nil {
			return nil, fmt.Errorf("dit: accept: %w", err)
		}

		// TODO(Joe-Degs):
		// is this right?, I dont really know man. Should this library
		// sort of package be discarding packets???? shouldn't this be
		// left for the server to decide?
		if op := opcode(buf[:n]); op != Rrq && op != Wrq {
			continue
		}

		// ephemeral port for the designated Conn as a unique TID
		srvTID, err := net.ResolveUDPAddr(clientTID.Network(), "localhost:0")
		if err != nil {
			return nil, fmt.Errorf("dit: accept: %w", err)
		}

		conn, err := net.ListenUDP(clientTID.Network(), srvTID)
		if err != nil {
			return nil, fmt.Errorf("dit: accept: %w", err)
		}

		// decode the new request
		request, err := DecodePacket(buf[:n])
		if err != nil {
			return nil, err
		}

		return &Conn{
			c:         conn,
			destTID:   clientTID.AddrPort(),
			connected: true,
			RequestBuffer: func() (*ReadWriteRequest, FileBufferFunc) {
				return NewFileBufferFunc(request.(*ReadWriteRequest))
			},
		}, nil
	}
	return nil, nil

}

// DestinationTID returns the destination address (transfer identifier)
// of a connection
func (c *Conn) DestinationTID() *netip.AddrPort {
	if c.connected {
		return &c.destTID
	}
	return nil
}

// SourceTID returns the source address (transfer identifier) of a connection
func (c *Conn) SourceTID() *netip.AddrPort {
	if c.connected {
		addr := c.c.LocalAddr()
		ipport, err := netip.ParseAddrPort(addr.String())
		if err != nil {
			return nil
		}
		return &ipport
	}
	return nil
}

// connect connects two endpoints wanting to send/recieve files from each other
//
// It takes two arguments; `in` buffer containing the request and `out` buffer
// to write response into.
// Its purpose is to send the initial packet that establishes connections
// (read/write packets) and wait for a response from the server. if it
// recieves a response, it keeps the address of the sender as the destination
// TID
func (c *Conn) connect(in []byte, out []byte) (n int, addr netip.AddrPort, err error) {
	// send request to server
	n, err = c.c.WriteToUDPAddrPort(in, c.destTID)
	if err != nil {
		return n, addr, fmt.Errorf("dit: connect write: %w", err)
	}

	// wait for response from a designated peer
	c.SetReadDeadline(10 * time.Second)
	n, addr, err = c.ReadFromAddrPort(out)
	c.connected = true
	c.destTID = addr
	return n, addr, err
}

// Write writes atmost len(n) bytes from b to the connection
//
// This method is only supposed to be called on connections, listening Conn's
// are not connections becuase they just listen for requests and never respond
// to them.
func (c *Conn) Write(b []byte) (int, error) {
	// write to specific connection if you are connected
	if c.connected && c.destTID.IsValid() {
		return c.c.WriteToUDPAddrPort(b, c.destTID)

	}
	return c.c.Write(b)
}

// ReadFrom waits and reads atmost len(b) bytes into b, returning the
// number of bytes written and the address of the sender or an error
func (c *Conn) ReadFrom(b []byte) (int, net.Addr, error) {
	return c.c.ReadFrom(b)
}

// ReadFromAddrPort waits and reads atmost len(b) bytes into b, returning
// the number of bytes written and the address of the sender or an error
func (c *Conn) ReadFromAddrPort(b []byte) (int, netip.AddrPort, error) {
	return c.c.ReadFromUDPAddrPort(b)
}

// Read reads atmost len(b) bytes from the connection return the number of
// bytes written or an error.
//
// When performing reads on Conn that is connected (actively sending/recieving
// data). It checks if the packet is from the peer it is connected to, if not
// it returns the ErrUnexpectedTID error which implies the packet should be
// ignored.
func (c *Conn) Read(b []byte) (int, error) {

	// if this is an active connection, but the write
	// is from a different TID return unexpected TID error
	if c.connected {
		n, addr, err := c.ReadFromAddrPort(b)
		if err == nil && addr != c.destTID {
			return n, ErrUnexpectedTID
		}
		return n, err
	}

	return c.c.Read(b)
}

// SetReadDeadline sets a deadline on reads from the TFTP server.
func (c *Conn) SetReadDeadline(n time.Duration) error {
	return c.c.SetReadDeadline(time.Now().Add(n))
}

// SetWriteDeadline sets a deadline on writes to the TFTP server.
func (c *Conn) SetWriteDeadline(n time.Duration) error {
	return c.c.SetWriteDeadline(time.Now().Add(n))
}

func (c *Conn) Close() error {
	return c.c.Close()
}

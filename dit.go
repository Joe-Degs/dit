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
	// ErrUnexpectedTID is returned if a Conn connected and actively
	// sending/recieving files recieves a packet from an other address
	ErrUnexpectedTID = errors.New("dit: packet from unexpected TID (host)")

	// ErrClientAccept is returned if the Accept method is accidentally called
	// on a client connection. Only listening connections (opened with the
	// Listen function) are allowed to wait and accept new client connections.
	ErrClientAccept = errors.New("dit: client cannot accept new connections")
)

// Conn is a tftp connection and providing functionality to send, recieve and
// serve files over the protocol
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

	// True if the Conn is a client actively reading/writing to another
	// client. False if Conn is a server and only listening for new connections
	connected bool

	// RequestBuffer is a function to return the request (that initiated a new
	// connection ) and a closure to get a buffered io object bound to an
	// underlying data stream when connected to a new client
	RequestBuffer func() (*ReadWriteRequest, FileBufferFunc)

	mu sync.Mutex
}

// Write writes atmost len(b) bytes from b into the connection. If the
// connection is actively sending/reading files from/to another client it writes
// to that specific host instead. Otherwise it's behaviour is specified by the
// net.Conn's Write method.
func (c *Conn) Write(b []byte) (int, error) {
	// write to specific connection if you are connected
	if c.connected && c.destTID.IsValid() {
		return c.c.WriteToUDPAddrPort(b, c.destTID)

	}
	return c.c.Write(b)
}

// Read tries to read len(b) bytes from the connection to b. If the connection
// is actively sending/reading files from/to another client, read only accepts
// reads from that host. It throws ErrUnexpectedTID if it gets data from a host
// other than the one it is actively connected to. Otherwise its behaviour
// conforms to that of net.Conn's Read method
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

// SetReadDeadline sets a deadline on reads from the TFTP server.
func (c *Conn) SetReadDeadline(n time.Duration) error {
	return c.c.SetReadDeadline(time.Now().Add(n))
}

// SetWriteDeadline sets a deadline on writes to the TFTP server.
func (c *Conn) SetWriteDeadline(n time.Duration) error {
	return c.c.SetWriteDeadline(time.Now().Add(n))
}

// Close the connection and resource associated with it.
func (c *Conn) Close() error {
	return c.c.Close()
}

// Addr returns the address of the underlying connection
func (c *Conn) Addr() net.Addr {
	return c.c.LocalAddr()
}

// Accept waits for new requests to the listening connection, creating new
// Conn's out of accepted requests and ignoring the others
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

// Dial connects to a TFTP server and returns a connection to the server which
// is a half open connection becuase with the TFTP protocol, the server creates
// a random udp connection to handle every new request that it accepts.
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

// newListenConn creates a new Conn object
func newListenConn(conn net.PacketConn) (*Conn, error) {
	udpConn, ok := conn.(*net.UDPConn)
	if !ok {
		return nil, fmt.Errorf("dit: only works over udp protocol: %T", conn)
	}
	return &Conn{
		c: udpConn,
	}, nil
}

// Listen announces on the network and returns a Conn that is capable of
// waiting for and accepting new requests to the listener.
//
// The Conn returned from Listen does not respond to any clients, it merely
// listens for read/write requests using the Accept method which returns
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

// ListenConfigConn is Listen but gives you more control over the behaviour
// of the underlying socket connection.
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

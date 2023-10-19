// dit implements the tftp protocol as specified in rfc 1350 at
// https://datatracker.ietf.org/doc/html/rfc1350
package dit

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/netip"
	"sync"
	"time"
)

var (
	// ErrUnexpectedTID is returned if a Conn connected and actively
	// sending/recieving files recieves a packet from an other address
	ErrUnexpectedTID = errors.New("packet from unexpected TID (host)")

	// ErrClientAccept is returned if the Accept method is accidentally called
	// on a client connection. Only listening connections (opened with the
	// Listen function) are allowed to wait and accept new client connections.
	ErrClientAccept = errors.New("client cannot accept new connections")
)

// Conn is a tftp connection and providing functionality to send, recieve and
// serve files over the protocol
type Conn struct {
	mu sync.Mutex
	// This is the primary connection, an active udp listener.
	// If the Conn is a client (connected=true) it accepts
	// packets from only destTID and writes only to it.
	// If the Conn is a listener/server (connected=false) it
	// can accept read/write requests from any addr and create a
	// new client connection to handle the request
	c *net.UDPConn

	// This holds the address that a client is actively connected to.
	destTID uint16

	// True if the Conn is a client actively reading/writing to another
	// client. False if Conn is a server and only listening for new connections
	connected bool
	req       *ReadWriteRequest
}

// Write writes atmost len(b) bytes from b into the connection. If the
// connection is actively sending/reading files from/to another client it writes
// to that specific host instead. Otherwise it's behaviour is specified by the
// net.Conn's Write method.
func (c *Conn) Write(b []byte) (int, error) {
	return c.c.Write(b)
}

func (c *Conn) WriteTo(b []byte, addr *net.UDPAddr) (int, error) {
	return c.c.WriteToUDP(b, addr)
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
		n, addr, err := c.ReadFrom(b)
		if err == nil && addr.Port() != c.destTID {
			return n, ErrUnexpectedTID
		}
		return n, err
	}

	return c.c.Read(b)
}

// ReadFrom waits and reads atmost len(b) bytes into b, returning the
// number of bytes written and the address of the sender or an error
func (c *Conn) ReadFrom(b []byte) (int, netip.AddrPort, error) {
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

func (c *Conn) Request() *ReadWriteRequest { return c.req }
func (c Conn) TID() uint16 {
	return c.destTID
}

func (c *Conn) AcceptRange(lo, hi uint16) (*Conn, error) {
	if c.connected {
		return nil, ErrClientAccept
	}

	var err error

	c.mu.Lock()
	defer c.mu.Unlock()

	// TODO(Joe-Degs): this seems like a bad way to do this. go see the
	// go package see how they allocate memory for accept
	buf := make([]byte, 256)
	for {
		n, raddr, err := c.c.ReadFromUDP(buf)
		if err != nil {
			return nil, fmt.Errorf("accept: %w", err)
		}

		if op := opcode(buf[:n]); op != Rrq && op != Wrq {
			_ = c.writeErrTo(IllegalOperation, "cannot perform operation", raddr)
			continue
		}

		req, err := Marshal(buf[:n])
		if err != nil {
			_ = c.writeErrTo(NotDefined, "could not decode packet", raddr)
			continue
		}

		conn, err := connectWithRange(lo, hi, raddr)
		if err != nil {
			err = c.writeErrTo(NotDefined, "could not connect", raddr)
			return nil, err
		}

		return &Conn{
			c:         conn,
			destTID:   raddr.AddrPort().Port(),
			connected: true,
			req:       req.(*ReadWriteRequest),
		}, nil
	}
	return nil, err
}

func (c *Conn) WriteErr(code ErrorCode, msg string) error {
	b, err := encode(Error, code, msg)
	if err != nil {
		return err
	}
	if _, err := c.Write(b); err != nil {
		return err
	}
	return nil
}

func (c *Conn) writeErrTo(code ErrorCode, msg string, addr *net.UDPAddr) error {
	b, err := encode(Error, code, msg)
	if err != nil {
		return err
	}
	if _, err := c.WriteTo(b, addr); err != nil {
		return err
	}
	return nil
}

// Accept waits for new requests to the listening connection, creating new
// Conn's out of accepted requests and ignoring the others
//
// This function is only supposed to be called on listening Conn's
func (c *Conn) Accept() (*Conn, error) {
	return c.AcceptRange(0, 0)
}

// given a range it will try to find a port (also the TID) in the range to connect with
func connectWithRange(lo, hi uint16, remote *net.UDPAddr) (conn *net.UDPConn, err error) {
	var local *net.UDPAddr

	if lo == 0 && hi == 0 {
		if local, err = net.ResolveUDPAddr(remote.Network(), ":0"); err != nil {
			return nil, err
		}
		if conn, err = net.DialUDP(remote.Network(), local, remote); err != nil {
			return nil, err
		}
		return
	}

	next := func() int { return rand.Intn(int(hi-lo+1)) + int(lo) }
	rand.Seed(time.Now().UnixNano())
	for i := 0; i > 10; i++ {
		addr := fmt.Sprintf(":%d", next())
		if local, err = net.ResolveUDPAddr(remote.Network(), addr); err != nil {
			continue
		}
		if conn, err = net.DialUDP(remote.Network(), local, remote); err != nil {
			continue
		} else {
			return
		}
	}

	return
}

// ListenConfigConn is Listen but gives you more control over the behaviour
// of the underlying socket connection.
// This makes it possible to do things like set platform specific socket options
// and adding a context to control lifetime of connections.
func ListenConfigConn(ctx context.Context, cfg *net.ListenConfig, address string) (*Conn, error) {
	conn, err := cfg.ListenPacket(ctx, "udp", address)
	if err != nil {
		return nil, err
	}
	return &Conn{c: conn.(*net.UDPConn)}, nil
}

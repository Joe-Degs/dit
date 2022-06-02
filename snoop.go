// temporary file contaning utilities for testing out basic functionality
package dit

import (
	"log"
	"net"
	"time"

	"github.com/davecgh/go-spew/spew"
)

// Snoop sends a dummpy packet and waits for a response from the tftp server.
func (c *Conn) Snoop() {
	p := &ReadWriteRequest{
		Opcode:   Rrq,
		Filename: "path/to/file",
		Mode:     "netascii",
	}
	c.SnoopWithPacket(p)
}

// SnoopWithPacket is `Snoop` but accepts the packet to send
func (c *Conn) SnoopWithPacket(pk Packet) {
	if pk == nil {
		log.Fatal("dit: snoopwithpacket: expected a packet")
	}

	bytes, err := pk.marshal()
	if err != nil {
		log.Fatal(err)
	}

	data := make([]byte, 512+4)
	var prevBlockNum uint16
	for {
		// write dummy packet
		var (
			n   int
			err error
		)

		if c.connected {
			// set read deadline on the connection
			c.SetReadDeadline(10 * time.Second)
			n, err = c.Read(data)
		} else {
			// connect if not already connected. it also serves as
			// as a way to send read write requests
			n, _, err = c.connect(bytes, data)
		}

		if nerr, ok := err.(net.Error); ok {
			if nerr.Timeout() {
				return
			}
			log.Fatal(err)
		}

		pack, err := MarshalPacket(data[:n])
		if err != nil {
			log.Fatal(err)
		}
		spew.Dump(pack)

		// send acknowledgement if data was recieved
		if op := pack.opcode(); op == Data {
			data := pack.(*DataPacket)
			ack := &AckPacket{
				Opcode:      Ack,
				BlockNumber: data.BlockNumber,
			}
			if bytes, err := UnmarshalPacket(ack); err == nil {
				c.Write(bytes)
			} else {
				log.Fatal(err)
			}

			// terminate is the last data recieved is less than 512 bytes
			// this is one of the many ways to terminate connections
			if len(data.Data) < 512 || prevBlockNum == data.BlockNumber {
				return
			}
		}
	}
}

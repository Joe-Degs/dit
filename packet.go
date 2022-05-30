// packet contains types and functions to marshal/unmarshal TFTP packets as
// described in RFC1350, apendix I.
package dit

import (
	"encoding/binary"
	"fmt"
	"unicode/utf8"
)

// Packet is a TFTP protocol packet
type Packet interface {
	opcode() Opcode
	marshal() ([]byte, error)
	unmarshal([]byte) error
}

func opcode(b []byte) Opcode {
	return Opcode(binary.BigEndian.Uint16(b[0:2]))
}

// TODO(Joe-Degs): change decode packet to MarshalPacket or something and
// remove the depency on encoding. U don't really need that.
func DecodePacket(b []byte) (Packet, error) {
	var p Packet
	switch op := opcode(b); op {
	case Rrq, Wrq:
		p = &ReadWriteRequest{Opcode: op}
	case Data:
		p = &DataPacket{Opcode: op}
	case Ack:
		p = &AckPacket{Opcode: op}
	case Error:
		p = &ErrorPacket{Opcode: op}
	default:
		return nil, fmt.Errorf("dit: opcode %d not recognized", op)
	}

	if err := p.unmarshal(b); err != nil {
		return nil, fmt.Errorf("dit: unmarshal packet: %w", err)
	}

	return p, nil
}

// MarshalPacket turns a structured packet to its binary representation
// for sending over the wire
func MarshalPacket(p Packet) ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("dit: cannot marshal nil packet")
	}
	return p.marshal()
}

// An Opcode encodes the type of the packet
type Opcode uint16

const (
	Rrq   Opcode = iota + 1 // A Read Request Type
	Wrq                     // A Write Request Type
	Data                    // Data Type
	Ack                     // Acknowledgement Type
	Error                   // Error Type
)

// ReadWriteRequest is a TFTP read/write request packet as described in RFC1350,
// apendix I
type ReadWriteRequest struct {
	Opcode   Opcode
	Filename string
	Mode     string
}

func (p *ReadWriteRequest) unmarshal(b []byte) error {
	// p.Opcode = Opcode(binary.BigEndian.Uint16(b[0:2]))

	// strings are null terminated bytes
	strs := b[2:]
	for i, s := range strs {

		// find the first null byte
		if s == 0 {
			// decode filename null terminated string
			if bytes := strs[:i]; len(bytes) >= 1 {
				if !utf8.Valid(bytes) {
					return fmt.Errorf("dit: filename contains illegal utf8 values, %s", bytes)
				}
				p.Filename = string(bytes)
			}

			// get the rest null terminated string as mode
			if bytes := strs[i+1 : len(strs)-1]; len(bytes) >= 1 {
				if !utf8.Valid(bytes) {
					return fmt.Errorf("dit: mode does not contain valid utf8, %s", bytes)
				}
				p.Mode = string(bytes)
			}
			break
		}

	}
	return nil
}

func (p *ReadWriteRequest) marshal() ([]byte, error) {
	data := make([]byte, 2)
	binary.BigEndian.PutUint16(data, uint16(p.Opcode))
	data = append(data, append([]byte(p.Filename), 0)...)
	data = append(data, append([]byte(p.Mode), 0)...)
	if len(data) != 2+len(p.Filename)+len(p.Mode)+2 {
		return nil, fmt.Errorf("dit: packet length not compatible with items")
	}
	return data, nil
}

func (p ReadWriteRequest) opcode() Opcode {
	return p.Opcode
}

// DataPacket is a TFTP data packet as described in RFC1350, apendix I
type DataPacket struct {
	Opcode      Opcode
	BlockNumber uint16
	Data        []byte
}

func (DataPacket) opcode() Opcode {
	return Data
}

func (p *DataPacket) unmarshal(b []byte) error {
	// p.Opcode = Opcode(binary.BigEndian.Uint16(b[0:2]))
	p.BlockNumber = binary.BigEndian.Uint16(b[2:4])

	if l := len(b[4:]); l > 0 {
		p.Data = make([]byte, l)
		if lc := copy(p.Data, b[4:]); lc != l {
			return fmt.Errorf("unmarshaling %d bytes failed", l)
		}
	}
	return nil
}

func (p *DataPacket) marshal() ([]byte, error) {
	data := make([]byte, 4)
	binary.BigEndian.PutUint16(data[0:2], uint16(p.Opcode))
	binary.BigEndian.PutUint16(data[2:4], p.BlockNumber)
	return append(data, p.Data...), nil
}

// AckPacket is a TFTP acknowledgement packet as described in RFC1350,apendix I
type AckPacket struct {
	Opcode      Opcode
	BlockNumber uint16
}

func (AckPacket) opcode() Opcode {
	return Ack
}

func (p *AckPacket) unmarshal(b []byte) error {
	// p.Opcode = Opcode(binary.BigEndian.Uint16(b[0:2]))
	p.BlockNumber = binary.BigEndian.Uint16(b[2:4])
	return nil
}

func (p *AckPacket) marshal() ([]byte, error) {
	data := make([]byte, 4)
	binary.BigEndian.PutUint16(data[0:2], uint16(p.Opcode))
	binary.BigEndian.PutUint16(data[2:4], p.BlockNumber)
	return data, nil
}

// ErrorCode represents a TFTP error code as specified in RFC1350, apendix I
type ErrorCode uint16

// TFTP error code constants as specified in RFC1350, apendix I
const (
	NotDefined ErrorCode = iota
	FileNotFound
	AccessViolation
	DiskFull
	IllegalOperation
	UnknownTID
	FileAlreadyExists
	NoSuchUser
)

// ErrorPacket is a TFTP error packet as described in RFC1350,apendix I
type ErrorPacket struct {
	Opcode    Opcode
	ErrorCode ErrorCode
	ErrMsg    string
}

func (ErrorPacket) opcode() Opcode {
	return Error
}

func (p *ErrorPacket) unmarshal(b []byte) error {
	// p.Opcode = Opcode(binary.BigEndian.Uint16(b[0:2]))
	p.ErrorCode = ErrorCode(binary.BigEndian.Uint16(b[2:4]))
	if bytes := b[4 : len(b)-1]; len(bytes) >= 1 {
		if !utf8.Valid(bytes) {
			return fmt.Errorf("dit: errmsg contains invalid utf8, %s", bytes)
		}
		p.ErrMsg = string(bytes)
	}
	return nil
}

func (p *ErrorPacket) marshal() ([]byte, error) {
	data := make([]byte, 4)
	binary.BigEndian.PutUint16(data[0:2], uint16(p.Opcode))
	binary.BigEndian.PutUint16(data[2:4], uint16(p.ErrorCode))
	return append(data, append([]byte(p.ErrMsg), 0)...), nil
}

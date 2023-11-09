// packet contains types and functions to marshal/unmarshal TFTP packets as
// described in RFC1350, RFC2347, RFC2348, RFC2349 and RFC7440
package dit

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Packet is a TFTP protocol packet
type Packet interface {
	opcode() Opcode
	marshal() ([]byte, error)
	unmarshal([]byte) error
}

// extract the opcode from a byte packet
func opcode(b []byte) Opcode {
	return Opcode(binary.BigEndian.Uint16(b[0:2]))
}

// MarshalPacket marshals a binary packet into a packet structure
func Marshal(b []byte) (Packet, error) {
	var p Packet
	switch op := opcode(b); op {
	case Rrq, Wrq:
		p = &ReadWriteRequest{Opcode: op}
	case Data:
		p = &DataPacket{Opcode: op}
	case Ack:
		p = &AckPacket{Opcode: op}
	case OAck:
		p = &OAckPacket{Opcode: op}
	case Error:
		p = &ErrorPacket{Opcode: op}
	default:
		return nil, fmt.Errorf("opcode %d not recognized", op)
	}

	if err := p.unmarshal(b); err != nil {
		return nil, fmt.Errorf("unmarshal packet: %w", err)
	}

	return p, nil
}

// UnmarshalPacket unmarshals a structured packet into its binary format
func Unmarshal(p Packet) ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("cannot marshal nil packet")
	}
	return p.marshal()
}

func encode(op Opcode, args ...any) ([]byte, error) {
	var p Packet
	switch op {
	case Error:
		p = &ErrorPacket{
			Opcode:    op,
			ErrorCode: args[0].(ErrorCode),
			ErrMsg:    args[1].(string),
		}
	default:
		return nil, fmt.Errorf("decode for %s not implemented", op)
	}
	b, err := p.marshal()
	if err != nil {
		return nil, err
	}
	return b, nil
}

// A TFTP protocol opcode as specified in rfc1350 and rfc2347
type Opcode uint16

const (
	Rrq   Opcode = iota + 1 // A Read Request Type
	Wrq                     // A Write Request Type
	Data                    // Data Type
	Ack                     // Acknowledgement Type
	Error                   // Error Type
	OAck                    // Optional Acknowlegdement type
)

// Optional extensions as specified in rfc2347, rfc2348, rfc2349 and rfc7440.
// The wire format of TFTP options are case insensitive null terminated strings
type Option uint8

const (
	// RFC2348
	Blksize Option = iota // block size option, valid values range from 8-65464 inclusive

	// RFC2349
	Timeout // timeout interval option, valid values range from 1-255 secs inclusive
	Tsize   // transfer size option, value is the size of file to be transfered

	// RFC7440
	//
	// windowsize option. this option specifies the number of blocks to transmit
	// before accepting an acknowledgment. valid values are between 1-65535
	// inclusive
	Windowsize

	// unknown to signal the server cannot parse the null terminated option
	// that it was presented
	Unknown
)

// ErrInvalidOptVal is returned if the value of an option is not between the
// range of accepted values.
var ErrInvalidOptVal = errors.New("dit: invalid option value")

func ValidateOptValue(opt Option, val string) (int, error) {
	valInt, err := strconv.Atoi(val)
	if err != nil {
		return valInt, err
	}

	switch opt {
	case Blksize:
		// valid values range from 8-65464
		if valInt >= 8 && valInt <= 65464 {
			return valInt, nil
		}
	case Timeout:
		// valid values range from 1-255 secs inclusive
		if valInt >= 1 && valInt <= 255 {
			return valInt, nil
		}
	case Tsize:
		return valInt, nil
	case Windowsize:
		// valid values are between 1-65535 inclusive
		if valInt >= 1 && valInt <= 65535 {
			return valInt, nil
		}
	}

	return 0, ErrInvalidOptVal
}

// MarshalOpts mashals an option string to its Option equivalent. It returns
// Unknown if the option string is not recognized
func MarshalOpts(opt string) Option {
	switch strings.ToLower(opt) {
	case "blksize":
		return Blksize
	case "timeout":
		return Timeout
	case "tsize":
		return Tsize
	case "windowsize":
		return Windowsize
	default:
		return Unknown
	}
}

// Unmarshal convert an Option to its string equivalent. It returns "unknown" if
// the option is not recognized.
func UnmarshalOpts(opt Option) string {
	switch opt {
	case Blksize:
		return "blksize"
	case Timeout:
		return "timeout"
	case Tsize:
		return "tsize"
	case Windowsize:
		return "windowsize"
	default:
		return "unknown"
	}
}

// ReadWriteRequest is a TFTP read/write request packet as described in RFC1350,
// apendix I
type ReadWriteRequest struct {
	Opcode   Opcode
	Filename string
	Mode     string

	// tftp option extensions are appended to the read/write
	// requests as null terminated string pairs (option => value)
	Options map[Option]int
}

// loop through a byte slice and retrieve all null terminated strings as
// proper golang utf8 string values
func getNullTerminatedStrings(strs []byte) ([]string, error) {
	var strVals []string

	// loop only if we have atleast one null terminated character
	if len(strs) >= 2 {
		// loop through byte slice and pick out all the strings in it
		var lastNull int
		for i, s := range strs {

			// if a null byte is encountered we read byte from last null position
			// to new null position, and keep it in a slice for later processing
			if s == 0 {
				if bytes := strs[lastNull:i]; len(bytes) >= 1 {
					if !utf8.Valid(bytes) {
						// returns the string values extracted so far if an
						// error is encountered while extracting
						return strVals, fmt.Errorf("dit: filename contains illegal utf8 values, %s", bytes)
					}
					strVals = append(strVals, string(bytes))
					lastNull = i + 1
				}
			}
		}
	}
	return strVals, nil
}

func (p *ReadWriteRequest) unmarshal(b []byte) error {
	strVals, err := getNullTerminatedStrings(b[2:])
	if err != nil {
		return err
	}

	// options are extensions and if there is a problem parsing one, it is not
	//  a reason to stop the parsing process, we continue to parse as much as
	//  we can and then return the errors encountered afterwards
	if len(strVals) >= 2 {
		// we got some filename, mode and probably options
		p.Filename = strVals[0]
		p.Mode = strVals[1]

		if optVals := strVals[2:]; len(optVals) >= 2 {
			options := make(map[Option]int)
			for i := 0; i < len(optVals); i += 2 {
				opt := MarshalOpts(optVals[i])
				if opt == Unknown {
					continue
				}
				var val int
				val, err = ValidateOptValue(opt, optVals[i+1])
				if err == nil {
					options[opt] = val
				}
			}

			// give the options to the request if we got some
			if len(options) >= 1 {
				p.Options = options
			}
		}
	}

	return err
}

// convert go string to null terminated string of bytes
func nullTerminate(s string) []byte {
	return append([]byte(s), 0)
}

func (p *ReadWriteRequest) marshal() ([]byte, error) {
	data := make([]byte, 2)
	binary.BigEndian.PutUint16(data, uint16(p.Opcode))
	data = append(data, nullTerminate(p.Filename)...)
	data = append(data, nullTerminate(p.Mode)...)
	if len(p.Options) >= 1 {
		for opt, val := range p.Options {
			valStr := strconv.Itoa(val)
			data = append(data, nullTerminate(UnmarshalOpts(opt))...)
			data = append(data, nullTerminate(valStr)...)
		}
	}
	return data, nil
}

func (p ReadWriteRequest) opcode() Opcode {
	return p.Opcode
}

// OAckPacket is an optional acknowledgement packet structure as specified in RFC2347
type OAckPacket struct {
	Opcode  Opcode
	Options map[Option]int
}

func (OAckPacket) opcode() Opcode {
	return OAck
}

func (p *OAckPacket) unmarshal(b []byte) error {
	if optVals, err := getNullTerminatedStrings(b[2:]); len(optVals) >= 2 {
		options := make(map[Option]int)
		for i := 0; i < len(optVals); i += 2 {
			opt := MarshalOpts(optVals[i])
			if opt == Unknown {
				continue
			}
			var val int
			val, err = ValidateOptValue(opt, optVals[i+1])
			if err == nil {
				options[opt] = val
			}
		}

		if len(options) >= 1 {
			p.Options = options
		}
	} else if err != nil {
		return err
	}

	return nil
}

func (p *OAckPacket) marshal() ([]byte, error) {
	data := make([]byte, 2)
	binary.BigEndian.PutUint16(data, uint16(p.Opcode))
	if len(p.Options) >= 1 {
		for opt, val := range p.Options {
			data = append(data, nullTerminate(UnmarshalOpts(opt))...)
			data = append(data, nullTerminate(strconv.Itoa(val))...)
		}
	}
	return data, nil
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
	p.BlockNumber = binary.BigEndian.Uint16(b[2:4])

	if l := len(b[4:]); l > 0 {
		p.Data = make([]byte, l)
		if lc := copy(p.Data, b[4:]); lc != l {
			return fmt.Errorf("dit: unable to copy all %d bytes", l)
		}
	}
	return nil
}

func (p *DataPacket) marshal() ([]byte, error) {
	data := make([]byte, 4, len(p.Data)+4)
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

	// RequestDenied was introduced in the tftp optional extension rfc2347. with
	// code "8" it is used to terminate a connection during option negotiation
	RequestDenied
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
	p.ErrorCode = ErrorCode(binary.BigEndian.Uint16(b[2:4]))
	if strVals, err := getNullTerminatedStrings(b[4:]); len(strVals) >= 1 {
		p.ErrMsg = strings.Join(strVals, " ")
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

func (p *ErrorPacket) marshal() ([]byte, error) {
	data := make([]byte, 4, len(p.ErrMsg)+5)
	binary.BigEndian.PutUint16(data[0:2], uint16(p.Opcode))
	binary.BigEndian.PutUint16(data[2:4], uint16(p.ErrorCode))
	data = append(data, nullTerminate(p.ErrMsg)...)
	return data, nil
}

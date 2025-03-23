package dit

import (
	"bytes"
	"testing"
)

func TestReadWriteRequest(t *testing.T) {
	tests := []struct {
		name     string
		req      ReadWriteRequest
		expected []byte
	}{
		{
			name: "simple read request",
			req: ReadWriteRequest{
				Opcode:   Rrq,
				Filename: "testfile.txt",
				Mode:     "octet",
			},
			// opcode (2 bytes) + filename + null + mode + null
			expected: []byte{0, 1, 't', 'e', 's', 't', 'f', 'i', 'l', 'e', '.', 't', 'x', 't', 0, 'o', 'c', 't', 'e', 't', 0},
		}, {
			name: "write request with options",
			req: ReadWriteRequest{
				Opcode:   Wrq,
				Filename: "outfile.bin",
				Mode:     "octet",
				Options: map[Option]int{
					Blksize: 1024,
					Timeout: 5,
				},
			},
			expected: []byte{0, 2, 'o', 'u', 't', 'f', 'i', 'l', 'e', '.', 'b', 'i', 'n', 0, 'o', 'c', 't', 'e', 't', 0,
				'b', 'l', 'k', 's', 'i', 'z', 'e', 0, '1', '0', '2', '4', 0, 't', 'i', 'm', 'e', 'o', 'u', 't', 0, '5', 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.req.marshal()
			if err != nil {
				t.Errorf("marshal error: %v", err)
				return
			}

			if !bytes.Equal(data, tt.expected) {
				t.Errorf("marshal failed:\nexpected %v, got %v", tt.expected, data)
			}

			// now lets unmarshal the data
			var testReq ReadWriteRequest
			testReq.Opcode = tt.req.Opcode
			if err := testReq.unmarshal(data); err != nil {
				t.Errorf("marshal error: %v", err)
				return
			}
			if testReq.Filename != tt.req.Filename {
				t.Errorf("unmarshal failed:\nexpected %v, got %v", tt.req.Filename, testReq.Filename)
			}
			if testReq.Mode != tt.req.Mode {
				t.Errorf("unmarshal failed:\nexpected %v, got %v", tt.req.Mode, testReq.Mode)
			}
			if len(testReq.Options) != len(tt.req.Options) {
				t.Errorf("options count mismatch: expetected %v, got %v", len(tt.req.Options), len(testReq.Options))
			} else {
				for opt, val := range tt.req.Options {
					if testReq.Options[opt] != val {
						t.Errorf("options %s mismatch: expected %d, got %d", UnmarshalOpts(opt), val, testReq.Options[opt])
					}
				}
			}
		})
	}
}

func TestDataPacket(t *testing.T) {
	testData := "tftp data packet test data"
	tests := []struct {
		name     string
		packet   DataPacket
		expected int
	}{
		{
			name: "empty data packet",
			packet: DataPacket{
				Opcode:      Data,
				BlockNumber: 42,
				Data:        []byte{},
			},
			expected: 4, // opcode + blocknumber
		},
		{
			name: "data packet with content",
			packet: DataPacket{
				Opcode:      Data,
				BlockNumber: 42,
				Data:        []byte(testData),
			},
			expected: 4 + len(testData), // opcode + blocknumber
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.packet.marshal()
			if err != nil {
				t.Errorf("marshal failed: %v", err)
			}

			if len(data) != tt.expected {
				t.Errorf("marshal data length mismatch: expected %v, got %v", tt.expected, len(data))
			}

			var testReq DataPacket
			testReq.Opcode = tt.packet.Opcode
			if err := testReq.unmarshal(data); err != nil {
				t.Errorf("marshal error: %v", err)
			}
			if testReq.BlockNumber != tt.packet.BlockNumber {
				t.Errorf("unmarshal failed:\nexpected %v, got %v", tt.packet.BlockNumber, testReq.BlockNumber)
			}
			if !bytes.Equal(testReq.Data, tt.packet.Data) {
				t.Errorf("data mismatch:\nexpected %v, got %v", tt.packet.Data, testReq.Data)
			}
		})
	}
}

func TestErrorPacket(t *testing.T) {
	tests := []struct {
		name     string
		packet   ErrorPacket
		expected []byte
	}{
		{
			name: "File not found error",
			packet: ErrorPacket{
				Opcode:    Error,
				ErrorCode: FileNotFound,
				ErrMsg:    "File not found",
			},
			expected: []byte{0, 5, 0, 1, 'F', 'i', 'l', 'e', ' ', 'n', 'o', 't', ' ', 'f', 'o', 'u', 'n', 'd', 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.packet.marshal()
			if err != nil {
				t.Errorf("marshal error: %v", err)
				return
			}

			if !bytes.Equal(data, tt.expected) {
				t.Errorf("marshalled data doesn't match: expected %v got %v", tt.expected, data)
			}

			var testReq ErrorPacket
			testReq.Opcode = Error
			err = testReq.unmarshal(data)
			if err != nil {
				t.Errorf("unmarshal error: %v", err)
				return
			}
			if testReq.ErrorCode != tt.packet.ErrorCode {
				t.Errorf("error code mismatch: expected %d, got %d", tt.packet.ErrorCode, testReq.ErrorCode)
			}
			if testReq.ErrMsg != tt.packet.ErrMsg {
				t.Errorf("error message mismatch: expected %s, got %s", tt.packet.ErrMsg, testReq.ErrMsg)
			}
		})
	}
}

func TestAckPacket(t *testing.T) {
	tests := []struct {
		name     string
		packet   AckPacket
		expected []byte
	}{
		{
			name: "Acknowledgment packet",
			packet: AckPacket{
				Opcode:      Ack,
				BlockNumber: 42,
			},
			expected: []byte{0, 4, 0, 42},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.packet.marshal()
			if err != nil {
				t.Errorf("marshal error: %v", err)
				return
			}

			if !bytes.Equal(data, tt.expected) {
				t.Errorf("marshalled data doesn't match: expected %v, got: %v", tt.expected, data)
			}

			// Test unmarshal
			var testReq AckPacket
			testReq.Opcode = Ack
			err = testReq.unmarshal(data)
			if err != nil {
				t.Errorf("unmarshal error: %v", err)
				return
			}
			if testReq.BlockNumber != tt.packet.BlockNumber {
				t.Errorf("block number mismatch: expected %d, got %d", tt.packet.BlockNumber, testReq.BlockNumber)
			}
		})
	}
}

func TestOAckPacket(t *testing.T) {
	tests := []struct {
		name   string
		packet OAckPacket
	}{
		{
			name: "option acknowledgment packet",
			packet: OAckPacket{
				Opcode: OAck,
				Options: map[Option]int{
					Blksize: 1024,
					Timeout: 5,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.packet.marshal()
			if err != nil {
				t.Errorf("marshal error: %v", err)
				return
			}

			var testReq OAckPacket
			testReq.Opcode = OAck
			err = testReq.unmarshal(data)
			if err != nil {
				t.Errorf("unmarshal error: %v", err)
				return
			}
			if len(testReq.Options) != len(tt.packet.Options) {
				t.Errorf("options count mismatch: expected %d, got %d", len(tt.packet.Options), len(testReq.Options))
			} else {
				for opt, val := range tt.packet.Options {
					if testReq.Options[opt] != val {
						t.Errorf("option %s mismatch: expected %d, got %d", UnmarshalOpts(opt), val, testReq.Options[opt])
					}
				}
			}
		})
	}
}

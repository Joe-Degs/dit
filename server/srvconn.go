package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Joe-Degs/dit"
)

type srvconn struct {
	*dit.Conn
	id  int64
	dir string
	log *logger
	cfg config
	buf *dit.FileBuffer
	f   *os.File

	negotiatedOpts map[dit.Option]int
}

func newsrvconn(dir string, log *logger, cfg config) *srvconn {
	return &srvconn{
		cfg:            cfg,
		log:            log,
		dir:            dir,
		buf:            dit.NewFileBuffer(),
		negotiatedOpts: make(map[dit.Option]int),
	}
}

func (s *srvconn) init() error {
	req := s.Request()
	filename := filepath.Join(s.dir, req.Filename)

	if s.buf.Is(filename) {
		return nil
	}

	_, err := os.Stat(filename)
	if err != nil {
		s.log.Error("stat error: %+v", err)
		var serr error
		switch {
		case errors.Is(err, os.ErrNotExist):
			if req.Opcode == dit.Rrq {
				serr = s.WriteErr(dit.FileNotFound, "file does not exist")
			}
			if req.Opcode == dit.Wrq && !s.cfg.Create {
				serr = s.WriteErr(dit.FileNotFound, "file does not exist and creation not allowed")
			}
		case errors.Is(err, os.ErrPermission):
			serr = s.WriteErr(dit.AccessViolation, "permision denied")
		default:
			serr = s.WriteErr(dit.NotDefined, "could not stat file")
		}

		if serr != nil {
			err = fmt.Errorf("%w: failed to send error: %w", err, serr)
			return err
		}

		// If we get here and it's a write request with Create=true and file doesn't exist,
		// we continue to the file opening logic
	}

	var flags int
	switch req.Opcode {
	case dit.Rrq:
		flags = os.O_RDONLY
	case dit.Wrq:
		flags = os.O_WRONLY | os.O_TRUNC
		if s.cfg.Create {
			flags |= os.O_CREATE
		}
	}

	f, err := os.OpenFile(filename, flags, fs.ModePerm)
	if err != nil {
		s.log.Error("open error: %+v", err)
		if e := s.WriteErr(dit.NotDefined, "could not stat file"); e != nil {
			return fmt.Errorf("%w: could not send error packet %w", err, e)
		}
		return err
	}

	s.f = f
	s.buf.WithRequest(req.Opcode, f)
	return nil
}

// start handles a TFTP connection with context-aware cancellation
func (s *srvconn) start(ctx context.Context, cl chan<- *srvconn) {
	defer func() { cl <- s }() // Always return to pool

	if err := s.init(); err != nil {
		s.Close()
		s.log.Error("failed to initialize connection: %v", err)
		return
	}

	// Handle the complete TFTP session with context
	if err := s.serve(ctx); err != nil {
		if !isContextCancelledError(err) {
			s.log.Error("serve error: %v", err)
		}
	}
}

// serve handles the complete TFTP session with context-aware cancellation
func (s *srvconn) serve(ctx context.Context) error {
	req := s.Request()
	s.log.Verbose("serving %s request for %s", req.Opcode, req.Filename)

	if err := s.handleOptions(ctx, req); err != nil {
		return err
	}

	// Handle the transfer based on request type
	switch req.Opcode {
	case dit.Rrq:
		return s.handleRead(ctx)
	case dit.Wrq:
		return s.handleWrite(ctx)
	default:
		return s.WriteErr(dit.IllegalOperation, "unsupported operation")
	}
}

// isContextCancelledError checks if the error is due to context cancellation
func isContextCancelledError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// Check for network errors that might be caused by context cancellation
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		if errors.Is(netErr.Err, context.Canceled) || errors.Is(netErr.Err, context.DeadlineExceeded) {
			return true
		}
	}
	return false
}

// readWithContext performs a context-aware read operation
func (s *srvconn) readWithContext(ctx context.Context, buf []byte) (int, error) {
	type result struct {
		n   int
		err error
	}

	resultCh := make(chan result, 1)
	go func() {
		n, err := s.Read(buf)
		resultCh <- result{n: n, err: err}
	}()

	// Wait for either the read to complete or context to be cancelled
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case res := <-resultCh:
		return res.n, res.err
	}
}

// handleOptions processes TFTP option negotiation (RFC2347) with context
func (s *srvconn) handleOptions(ctx context.Context, req *dit.ReadWriteRequest) error {
	s.negotiatedOpts[dit.Blksize] = s.cfg.BlockSize
	s.negotiatedOpts[dit.Timeout] = s.cfg.Timeout
	s.negotiatedOpts[dit.Windowsize] = 1

	for opt, clientValue := range req.Options {
		if s.cfg.Refuse != "" && dit.UnmarshalOpts(opt) == s.cfg.Refuse {
			s.log.Verbose("refusing option %s as configured", s.cfg.Refuse)
			continue
		}

		switch opt {
		case dit.Blksize:
			negotiatedSize := min(s.cfg.BlockSize, max(clientValue, 512))
			s.negotiatedOpts[opt] = negotiatedSize
			s.log.Verbose("negotiated blksize: client=%d, server_max=%d, final=%d", clientValue, s.cfg.BlockSize, negotiatedSize)
		case dit.Tsize:
			// For tsize, we echo back what client sent (it's informational)
			s.negotiatedOpts[opt] = clientValue
		case dit.Timeout:
			// Negotiate timeout: use minimum of client request and server config
			negotiatedTimeout := min(s.cfg.Timeout, max(clientValue, 1))
			s.negotiatedOpts[opt] = negotiatedTimeout
			s.log.Verbose("negotiated timeout: client=%d, server_max=%d, final=%d", clientValue, s.cfg.Timeout, negotiatedTimeout)
		case dit.Windowsize:
			serverMax := 16
			negotiatedSize := min(max(clientValue, 1), serverMax)
			s.negotiatedOpts[opt] = negotiatedSize
			s.log.Verbose("negotiated windowsize: client=%d, server_max=%d, final=%d", clientValue, serverMax, negotiatedSize)
		default:
			s.log.Verbose("unknown option %s=%d, ignoring", dit.UnmarshalOpts(opt), clientValue)
		}
	}

	oack := &dit.OAckPacket{
		Opcode:  dit.OAck,
		Options: s.negotiatedOpts,
	}

	data, err := dit.Unmarshal(oack)
	if err != nil {
		return s.WriteErr(dit.NotDefined, "failed to create OACK")
	}

	_, err = s.Write(data)
	if err != nil {
		return err
	}
	s.log.Verbose("sent OACK with negotiated options: %+v", s.negotiatedOpts)

	// According to RFC2347, after sending OACK:
	// - For READ operations: client sends ACK(0), then server sends DATA(1)
	// - For WRITE operations: client sends DATA(1) directly (no ACK(0))
	if req.Opcode == dit.Rrq {
		return s.waitForAck(ctx, 0)
	}
	// For write operations, we don't wait for ACK(0)
	return nil
}

type windowPacket struct {
	blockNum   uint16
	fileOffset int64
	len        int
}

// handleRead implements the complete read transfer following TFTP protocol with context
func (s *srvconn) handleRead(ctx context.Context) error {
	blockNum := uint16(1)
	filePos := int64(0)

	blockSize := s.negotiatedOpts[dit.Blksize]
	s.log.Verbose("using blocksize %d for read transfer", blockSize)
	windowSize := s.negotiatedOpts[dit.Windowsize]
	s.log.Verbose("using windowsize %d for read transfer", windowSize)

	for {
		window := make([]windowPacket, 0, windowSize)
		lastAckedBlock := uint16(0)
		isEOF := false

		// Send window of packets
		for i := 0; i < windowSize; i++ {
			startPos := filePos

			data := make([]byte, blockSize)
			n, err := s.buf.ReadNext(data)
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				s.log.Error("read error: %v", err)
				return s.WriteErr(dit.NotDefined, "read error")
			}

			if _, err := s.sendData(blockNum, data[:n]); err != nil {
				return err
			}

			window = append(window, windowPacket{
				blockNum:   blockNum,
				fileOffset: startPos,
				len:        n,
			})

			filePos += int64(n)
			blockNum++

			// Check for EOF
			if n < blockSize || err == io.EOF {
				isEOF = true
				break
			}
		}

		// Wait for ACK with selective retransmission
		expectedACK := window[len(window)-1].blockNum
		if err := s.waitForAckWithSelectiveRetransmit(ctx, expectedACK, window, &lastAckedBlock); err != nil {
			return err
		}

		// Check if transfer is complete
		if isEOF {
			s.log.Verbose("read transfer complete, %d blocks sent", expectedACK)
			return nil
		}
	}
}

// handleWrite implements the complete write transfer following TFTP protocol with context
func (s *srvconn) handleWrite(ctx context.Context) error {
	// Note: If options were negotiated, handleOptions() already waited for ACK(0)
	// For writes without options, we need to send ACK(0) to start the transfer
	req := s.Request()
	if len(req.Options) == 0 {
		if err := s.sendAck(0); err != nil {
			return err
		}
	}

	blockNum := uint16(1)

	// Determine the effective blocksize for this transfer
	// If client negotiated blocksize, use that; otherwise use default 512
	blockSize := s.negotiatedOpts[dit.Blksize]

	for {
		s.log.Verbose("waiting for data packet %d", blockNum)

		// Wait for data packet with retries
		data, receivedBlockNum, err := s.receiveDataPacketWithRetries(ctx, blockNum)
		if err != nil {
			s.log.Error("receiveDataPacketWithRetries failed: %v", err)
			return err
		}

		s.log.Verbose("received data packet %d with %d bytes", receivedBlockNum, len(data))

		// Verify block number
		if receivedBlockNum != blockNum {
			s.log.Verbose("unexpected block number: got %d, expected %d", receivedBlockNum, blockNum)
			continue // Ignore duplicate or out-of-order packets
		}

		// Write data to file
		if len(data) > 0 {
			s.log.Verbose("writing %d bytes to file", len(data))
			if _, err := s.buf.WriteNext(data); err != nil {
				s.log.Error("write to file failed: %v", err)
				return s.WriteErr(dit.NotDefined, "write error")
			}
		}

		// Send ACK
		s.log.Verbose("sending ACK for block %d", blockNum)
		if err := s.sendAck(blockNum); err != nil {
			s.log.Error("sendAck failed: %v", err)
			return err
		}

		// Check if transfer is complete (last block is < blockSize)
		if len(data) < blockSize {
			s.log.Verbose("write transfer complete, %d blocks received", blockNum)
			// Flush the buffer to ensure all data is written to file
			if err := s.buf.Close(); err != nil {
				s.log.Error("failed to flush buffer: %v", err)
				return s.WriteErr(dit.NotDefined, "write flush error")
			}
			return nil
		}
		blockNum++
	}
}

// sendDataAndWaitAck encapsulates the send-data-wait-ack cycle with context
func (s *srvconn) sendDataAndWaitAck(ctx context.Context, blockNum uint16, data []byte) error {
	packetData, err := s.sendData(blockNum, data)
	if err != nil {
		return err
	}
	return s.waitForAckWithRetries(ctx, blockNum, packetData)
}

func (s *srvconn) sendData(blockNum uint16, data []byte) (packetData []byte, err error) {
	dataPacket := &dit.DataPacket{
		Opcode:      dit.Data,
		BlockNumber: blockNum,
		Data:        data,
	}

	if packetData, err = dit.Unmarshal(dataPacket); err != nil {
		if err = s.WriteErr(dit.NotDefined, "failed to marshal data packet"); err != nil {
			return nil, err
		}
	}

	if _, err = s.Write(packetData); err != nil {
		return nil, err
	}
	s.log.Verbose("sent data block %d (%d bytes)", blockNum, len(data))
	return
}

// waitForAckWithSelectiveRetransmit waits for ACK with selective retransmission support
func (s *srvconn) waitForAckWithSelectiveRetransmit(ctx context.Context, expectedACK uint16, window []windowPacket, lastAckedBlock *uint16) error {
	const maxRetries = 3
	baseTimeout := time.Duration(s.cfg.Retransmit) * time.Microsecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		timeout := baseTimeout * time.Duration(1<<attempt)
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		// Check if parent context is already cancelled
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Try to receive ACK
		buf := make([]byte, 256)
		n, err := s.readWithContext(attemptCtx, buf)

		if err != nil {
			// Check if it's a timeout error
			if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
				s.log.Verbose("ACK timeout on attempt %d/%d for block %d, performing selective retransmission...", attempt+1, maxRetries, expectedACK)

				// Selective retransmission: retransmit packets after lastAckedBlock
				for _, pkt := range window {
					if pkt.blockNum > *lastAckedBlock {
						if err := s.retransmitFromFile(ctx, pkt); err != nil {
							s.log.Error("retransmission failed for block %d: %v", pkt.blockNum, err)
							return err
						}
					}
				}
				continue // Try again
			}
			// Non-timeout error, fail immediately
			s.log.Error("read error during ACK wait: %v", err)
			return err
		}

		// Parse the received packet
		packet, err := dit.Marshal(buf[:n])
		if err != nil {
			s.log.Error("packet parsing failed: %v", err)
			return s.WriteErr(dit.NotDefined, "invalid packet received")
		}

		ackPacket, ok := packet.(*dit.AckPacket)
		if !ok {
			s.log.Error("expected ACK packet, got %T", packet)
			return s.WriteErr(dit.IllegalOperation, "expected ACK packet")
		}

		// Update lastAckedBlock and check if we got the expected ACK
		*lastAckedBlock = ackPacket.BlockNumber
		if ackPacket.BlockNumber == expectedACK {
			s.log.Verbose("received ACK for block %d after %d attempts", expectedACK, attempt+1)
			return nil
		} else if ackPacket.BlockNumber < expectedACK {
			s.log.Verbose("received partial ACK %d, expected %d - some packets lost", ackPacket.BlockNumber, expectedACK)
			// Continue trying - we'll retransmit missing packets on next timeout
		} else {
			s.log.Verbose("received unexpected ACK: got %d, expected %d", ackPacket.BlockNumber, expectedACK)
		}
	}

	// All retries exhausted
	s.log.Error("max retries (%d) exhausted waiting for ACK %d", maxRetries, expectedACK)
	return s.WriteErr(dit.NotDefined, "retransmission timeout")
}

// retransmitFromFile reads data from file at specified offset and retransmits it
func (s *srvconn) retransmitFromFile(ctx context.Context, pkt windowPacket) error {
	// Seek to the packet's file position (os.File implements io.Seeker)
	if _, err := s.f.Seek(pkt.fileOffset, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to offset %d: %w", pkt.fileOffset, err)
	}

	// Read the data from file
	data := make([]byte, pkt.len)
	n, err := s.f.Read(data)
	if err != nil {
		return fmt.Errorf("failed to read data for retransmission: %w", err)
	}
	if n != pkt.len {
		return fmt.Errorf("retransmission read mismatch: expected %d bytes, got %d", pkt.len, n)
	}

	// Send the packet
	_, err = s.sendData(pkt.blockNum, data)
	if err != nil {
		return fmt.Errorf("failed to send retransmitted packet: %w", err)
	}

	s.log.Verbose("retransmitted block %d (%d bytes) from file offset %d", pkt.blockNum, pkt.len, pkt.fileOffset)
	return nil
}

// waitForAckWithRetries waits for ACK with retransmission logic and context
func (s *srvconn) waitForAckWithRetries(ctx context.Context, expectedBlockNum uint16, dataPacket []byte) error {
	const maxRetries = 3
	baseTimeout := time.Duration(s.cfg.Retransmit) * time.Microsecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		timeout := baseTimeout * time.Duration(1<<attempt) // 1x, 2x, 4x...
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		// Check if parent context is already cancelled
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// try to receive ACK
		buf := make([]byte, 256)
		n, err := s.readWithContext(attemptCtx, buf)

		if err != nil {
			// check if it's a timeout error
			if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
				s.log.Verbose("ACK timeout on attempt %d/%d for block %d, retransmitting...", attempt+1, maxRetries, expectedBlockNum)

				// retransmit the data packet
				if _, retryErr := s.Write(dataPacket); retryErr != nil {
					s.log.Error("retransmission failed: %v", retryErr)
					return retryErr
				}
				s.log.Verbose("retransmitted data block %d", expectedBlockNum)
				continue
			}
			// non-timeout error, fail immediately
			s.log.Error("read error during ACK wait: %v", err)
			return err
		}

		// Parse the received packet
		packet, err := dit.Marshal(buf[:n])
		if err != nil {
			s.log.Error("packet parsing failed: %v", err)
			return s.WriteErr(dit.NotDefined, "invalid packet received")
		}

		ackPacket, ok := packet.(*dit.AckPacket)
		if !ok {
			s.log.Error("expected ACK packet, got %T", packet)
			return s.WriteErr(dit.IllegalOperation, "expected ACK packet")
		}

		if ackPacket.BlockNumber == expectedBlockNum {
			s.log.Verbose("received ACK for block %d after %d attempts", expectedBlockNum, attempt+1)
			return nil
		} else {
			s.log.Verbose("unexpected ACK: got %d, expected %d", ackPacket.BlockNumber, expectedBlockNum)
			// Continue waiting for the correct ACK
		}
	}

	// All retries exhausted
	s.log.Error("max retries (%d) exhausted waiting for ACK %d", maxRetries, expectedBlockNum)
	return s.WriteErr(dit.NotDefined, "retransmission timeout")
}

// waitForAck waits for an ACK packet with the expected block number using context
func (s *srvconn) waitForAck(ctx context.Context, expectedBlockNum uint16) error {
	buf := make([]byte, 256)
	s.log.Verbose("waiting for ACK %d, reading from client...", expectedBlockNum)

	// Create context with timeout
	timeout := time.Duration(s.cfg.Timeout) * time.Second
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	n, err := s.readWithContext(timeoutCtx, buf)
	if err != nil {
		s.log.Error("waitForAck read failed: %v", err)
		return err
	}

	s.log.Verbose("waitForAck received %d bytes, parsing packet", n)
	packet, err := dit.Marshal(buf[:n])
	if err != nil {
		s.log.Error("waitForAck packet parsing failed: %v", err)
		return s.WriteErr(dit.NotDefined, "invalid packet received")
	}

	s.log.Verbose("waitForAck packet type: %T", packet)
	ackPacket, ok := packet.(*dit.AckPacket)
	if !ok {
		s.log.Error("waitForAck expected ACK packet, got %T", packet)
		return s.WriteErr(dit.IllegalOperation, "expected ACK packet")
	}

	if ackPacket.BlockNumber != expectedBlockNum {
		s.log.Verbose("unexpected ACK: got %d, expected %d", ackPacket.BlockNumber, expectedBlockNum)
		// For duplicate ACKs (already acknowledged packets), this is normal
		// For future ACKs, this might indicate packet loss - but we'll just continue
		// The TFTP client is responsible for detecting missing data and requesting retransmission
	}

	s.log.Verbose("received ACK for block %d", expectedBlockNum)
	return nil
}

// sendAck sends an ACK packet
func (s *srvconn) sendAck(blockNum uint16) error {
	ackPacket := &dit.AckPacket{
		Opcode:      dit.Ack,
		BlockNumber: blockNum,
	}

	data, err := dit.Unmarshal(ackPacket)
	if err != nil {
		return err
	}

	_, err = s.Write(data)
	s.log.Verbose("sent ACK for block %d", blockNum)
	return err
}

// receiveDataPacketWithRetries waits for data packet with retransmission logic and context
func (s *srvconn) receiveDataPacketWithRetries(ctx context.Context, expectedBlockNum uint16) ([]byte, uint16, error) {
	const maxRetries = 5
	baseTimeout := time.Duration(s.cfg.Retransmit) * time.Microsecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Calculate timeout with exponential backoff
		timeout := baseTimeout * time.Duration(1<<attempt) // 1x, 2x, 4x, 8x, 16x

		// Create context with timeout for this attempt
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		// Check if parent context is already cancelled
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		default:
		}

		// Try to receive data packet with context-aware reading
		buf := make([]byte, 65536) // Max TFTP packet size
		n, err := s.readWithContext(attemptCtx, buf)

		if err != nil {
			// Check if it's a timeout error
			if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
				s.log.Verbose("data timeout on attempt %d/%d for block %d, retransmitting ACK...", attempt+1, maxRetries, expectedBlockNum)

				// Retransmit the last ACK to prompt client retransmission
				if retryErr := s.sendAck(expectedBlockNum - 1); retryErr != nil {
					s.log.Error("ACK retransmission failed: %v", retryErr)
					return nil, 0, retryErr
				}
				s.log.Verbose("retransmitted ACK for block %d", expectedBlockNum-1)
				continue // Try again
			}
			// Non-timeout error, fail immediately
			s.log.Error("read error during data wait: %v", err)
			return nil, 0, err
		}

		// Parse the received packet
		packet, err := dit.Marshal(buf[:n])
		if err != nil {
			s.log.Error("packet parsing failed: %v", err)
			return nil, 0, s.WriteErr(dit.NotDefined, "invalid packet received")
		}

		dataPacket, ok := packet.(*dit.DataPacket)
		if !ok {
			s.log.Error("expected data packet, got %T", packet)
			return nil, 0, s.WriteErr(dit.IllegalOperation, "expected data packet")
		}

		s.log.Verbose("received data packet: block=%d, data_len=%d after %d attempts", dataPacket.BlockNumber, len(dataPacket.Data), attempt+1)
		return dataPacket.Data, dataPacket.BlockNumber, nil
	}

	// All retries exhausted
	s.log.Error("max retries (%d) exhausted waiting for data block %d", maxRetries, expectedBlockNum)
	return nil, 0, s.WriteErr(dit.NotDefined, "data reception timeout")
}

func (s *srvconn) end() *srvconn {
	s.buf.Reset() // reset buffer
	if s.f != nil {
		s.f.Seek(0, 0) // seek back to beginning of file
	}
	s.Conn.Close()
	return s
}

func (s *srvconn) Close() (err error) {
	if s.f != nil {
		err = s.f.Close()
	}
	if err1 := s.Conn.Close(); err1 != nil {
		err = err1
	}
	return
}

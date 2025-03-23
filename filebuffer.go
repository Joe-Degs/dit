package dit

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FileBuffer embeds a buffering IO object from the bufio package. It implements
// an io.ReadWriteCloser object and has a temporary buffer for the most recent
// io operation it has witnessed.
type FileBuffer struct {
	// r/w is a buffered reader/writer. the underlying type of the buffered
	// object is determined by whether this is used for reading/writing to
	// the underlying data source
	r *bufio.Reader
	w *bufio.Writer

	// the file so we can close it when we are done
	f io.ReadWriteCloser

	// buf keeps the most recents data read/written from/to the underlying data
	// source for retransmission
	buf *bytes.Buffer
}

// NewFileBuffer creates a new FileBuffer with an initialized temporary buffer
// for storing recently read/written data that might need retransmission.
func NewFileBuffer() *FileBuffer {
	return &FileBuffer{buf: new(bytes.Buffer)}
}

// WithRequest initializes the FileBuffer with the appropriate reader or writer
// based on the TFTP operation type (read or write request).
// It stores a reference to the file and creates either a buffered reader or writer.
//
// op must be either Rrq (read request) or Wrq (write request).
// file is the underlying file to read from or write to.
func (f *FileBuffer) WithRequest(op Opcode, file io.ReadWriteCloser) {
	f.f = file
	switch op {
	case Rrq:
		f.r = bufio.NewReader(file)
	case Wrq:
		f.w = bufio.NewWriter(file)
	}
	// TODO(joe): handle default case?
}

// Is checks if the buffer's underlying file has the specified name.
// It compares only the base filename (not the full path), returning true
// if they match and false if they don't or if the underlying object is not an os.File.
//
// This function is used to check if the buffer is already associated with a specific file
// before creating a new association.
func (f *FileBuffer) Is(name string) bool {
	fi, ok := f.f.(*os.File)
	if !ok {
		return false
	}
	return filepath.Base(fi.Name()) == filepath.Base(name)
}

// Reset clears the temporary buffer that stores the most recent data
// read from or written to the underlying file.
//
// This should be called after a successful ACK is received and retransmission
// of the current block is no longer needed, or when preparing to handle a new
// TFTP request with the same buffer.
func (f *FileBuffer) Reset() {
	f.buf.Reset()
}

// Read attempts to read exactly len(b) bytes from the underlying buffered reader
// into b. It returns the number of bytes read and an error if fewer than len(b)
// bytes were read.
//
// It returns io.EOF if no bytes could be read and io.ErrUnexpectedEOF if EOF was
// encountered before reading len(b) bytes. Calls io.ReadFull under the hood.
//
// Note: This method does NOT update the retransmission buffer. Use ReadNext if you
// need to store data for potential retransmission.
func (f *FileBuffer) Read(b []byte) (int, error) {
	// return io.ReadFull(f.r, b)
	return f.r.Read(b)
}

// Write attempts to write len(b) bytes from b to the underlying buffered writer.
// It returns the number of bytes written and any error encountered.
//
// Note: This method does NOT update the retransmission buffer. Use WriteNext if you
// need to store data for potential retransmission.
//
// The bytes aren't necessarily flushed to the underlying file until Close is called
// or the buffer is full.
func (f *FileBuffer) Write(b []byte) (int, error) {
	return f.w.Write(b)
}

// ReadNext attempts to read exactly len(b) bytes from the underlying file
// into b, storing the read data in a temporary buffer for potential retransmission.
//
// It returns the number of bytes read and any error encountered during reading.
// If an error occurs while storing data for retransmission, the original error
// from the read operation will be wrapped with additional context.
//
// This is the primary method for reading data in TFTP operations where
// retransmission might be needed.
func (f *FileBuffer) ReadNext(b []byte) (int, error) {
	read, err := f.Read(b)

	if read > 0 {
		f.buf.Reset()
		if n, err := f.buf.Write(b[:read]); err != nil {
			return read, fmt.Errorf("dit: err writting to retransmission buffer: %w", err)
		} else if read != n {
			return read, fmt.Errorf("dit: restransmision buffer write expected %d bytes, wrote %d bytes", read, n)
		}
	}

	return read, err
}

// WriteNext attempts to write len(b) bytes from b to the underlying file,
// storing the written data in a temporary buffer for potential retransmission.
//
// It returns the number of bytes written and any error encountered during writing.
// If an error occurs while storing data for retransmission, the original error
// from the write operation will be wrapped with additional context.
//
// This is the primary method for writing data in TFTP operations where
// retransmission might be needed.
func (f *FileBuffer) WriteNext(b []byte) (int, error) {
	wrote, err := f.Write(b)

	if wrote > 0 {
		if n, err := f.buf.Write(b[:wrote]); err != nil {
			return wrote, fmt.Errorf("dit: retransmission buffer write: %w", err)
		} else if n != wrote {
			return wrote, fmt.Errorf("dit: retransmission buffer write: expected %d, got %d", wrote, n)
		}
	}

	return wrote, err
}

// BufferLen returns the length of the temporary buffer storing the most
// recent data read from or written to the underlying file.
//
// This is useful for determining the size of data available for retransmission.
func (f *FileBuffer) BufferLen() int {
	return f.buf.Len()
}

// ReadBuffer copies data from the temporary retransmission buffer into b and
// returns the number of bytes copied.
//
// To retrieve all data in the buffer, ensure that len(b) >= f.BufferLen().
// If len(b) < f.BufferLen(), only the first len(b) bytes will be copied.
//
// This method is typically used when retransmission is required.
func (f *FileBuffer) ReadBuffer(b []byte) int {
	return copy(b, f.buf.Bytes())
}

// BufferedObject returns the underlying buffered reader or writer that was
// created based on the TFTP request type.
//
// It returns a *bufio.Reader for read requests (Rrq) or a *bufio.Writer
// for write requests (Wrq).
//
// This method is primarily used for internal access to the buffered I/O objects.
func (f *FileBuffer) BufferedObject() any {
	if f.r != nil {
		return f.r
	}
	return f.w
}

// Close flushes any buffered data to the underlying file and cleans up
// resources associated with the FileBuffer.
//
// For write requests, it flushes the buffered writer to ensure all data
// is written to the file. For read requests, it clears the retransmission buffer.
//
// Note: This method does NOT close the underlying file; that responsibility is left to the caller
func (f *FileBuffer) Close() error {
	if f.w != nil {
		if err := f.w.Flush(); err != nil {
			return err
		}
	}
	f.buf.Reset()
	return nil
}

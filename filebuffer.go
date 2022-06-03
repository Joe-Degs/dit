package dit

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
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

	// buf keeps the most recents data read/written from/to the underlying data
	// source for retransmission
	buf *bytes.Buffer
}

// FileBufferFunc is a function that takes a file path and creates a buffered io
// object with the file as the underlying data source/storage
//
// TFTP servers are allowed to serve files from any directory(not only the root
// filesystem). So paths are relative to the directory from which files are
// being served. The servers have a more extensive and proper view of what
// the paths are supposed to mean so let them decide the path they want to use
// by providing this type which is just a function
type FileBufferFunc func(path string) (*FileBuffer, error)

// NewFileBufferFunc returns the request and a closure to open/create file and
// embed it in a buffered io object for efficient reading/writing operations
func NewFileBufferFunc(req *ReadWriteRequest) (*ReadWriteRequest, FileBufferFunc) {
	var (
		f          *os.File
		err        error
		bufferFunc FileBufferFunc
		buf        = &FileBuffer{buf: new(bytes.Buffer)}
	)

	if op := req.opcode(); op == Rrq {
		// closure opens file and embeds it in a bufio.Reader for buffered io
		// ops
		bufferFunc = func(path string) (*FileBuffer, error) {
			if f, err = os.Open(path); err != nil {
				return nil, err
			}
			buf.r = bufio.NewReader(f)
			return buf, nil
		}
	} else if op == Wrq {
		// closure creates file and embeds it in bufio.Writer for writing
		bufferFunc = func(path string) (*FileBuffer, error) {
			if f, err = os.Create(path); err != nil {
				return nil, err
			}
			buf.w = bufio.NewWriter(f)
			return buf, nil
		}
	}

	return req, bufferFunc
}

// Read tries to read exactly len(b) from the underlying buffered io object into
// b. If returns the the number of bytes copied and an error if fewer
// than len(b) bytes were read. It returns an io.EOF if no bytes are read and
// an io.ErrUnexpectedEOF if an io.EOF is ecountered while reading from source
func (f *FileBuffer) Read(b []byte) (int, error) {
	return io.ReadFull(f.r, b)
}

// Write tries to write len(p) bytes to the underlying data stream. the
// behaviour of this funtion is defined by io.Writer and bufio.Writer.Write
func (f *FileBuffer) Write(b []byte) (int, error) {
	return f.w.Write(b)
}

// ReadNext tries to return the next set of len(b) bytes from the
// underlying data source. Keeping the bytes read in a temporary buffer
// incase there is the need to retransmit it.
func (f *FileBuffer) ReadNext(b []byte) (int, error) {
	// read next bunch of bytes from underlying storage
	read, err := f.Read(b)

	// reset the temporary buffer and copy bytes from underlying data
	// source into it. writing only the bytes read from storage
	if read > 0 {
		f.buf.Reset()
		if n, err := f.buf.Write(b[:read]); err != nil {
			return read, fmt.Errorf("dit: err writting to tmp buffer: %w", err)
		} else if read != n {
			return read, fmt.Errorf("dit: tmp buffer write exp %d bytes, wrote %d bytes", read, n)
		}
	}

	// at this stage we have either;
	// 1. read exactly len(b) bytes and have written it to tmp buffer
	// 2. read less than len(b) bytes and have written it to tmp buffer
	// 3. read nothing and written nothing to tmp buffer
	return read, err
}

// WriteNext tries to write the next set of len(p) bytes to the underlying data
// stream, keeping the same amount of bytes written in a temporary buffer.
// It returns the number of bytes written from p if the write stopped early,
// if the write to the temporary buffer results in an error or if the number
// of bytes written to temporary buffer is less than the number written to
// underlying data source
func (f *FileBuffer) WriteNext(b []byte) (int, error) {
	// try to write len(b) bytes to the underlying storage
	wrote, err := f.Write(b)

	// if we wrote something, we have to keep what was written in the
	// underlying storage for keeps
	if wrote > 0 {
		if n, err := f.buf.Write(b[:wrote]); err != nil {
			return wrote, fmt.Errorf("dit: tmp buffer write: %w", err)
		} else if n != wrote {
			return wrote, fmt.Errorf("dit: tmp buffer write: expected %d, got %d", wrote, n)
		}
	}

	// at this stage we have either;
	// 1. successfully written everything to the underlying storage and tmp
	//       buffer
	// 2. written something to underlying storage and tmp buffer
	// 3. written nothing to underlying storage and tmp buffer
	// either we stop and return the errors and bytes written
	return wrote, err
}

// BufferLen returns the length of the temporary buffer storing the most
// recent data from/to the underlying data stream
func (f *FileBuffer) BufferLen() int {
	return f.buf.Len()
}

// ReadBuffer tries to copy len(b) bytes from the temporary buffer into b and
// returns the number of bytes copied
//
// if you want exactly all the amount of data in the buffer then you have
// to supply a buffer with length >= f.BufferLen()
func (f *FileBuffer) ReadBuffer(b []byte) int {
	return copy(b, f.buf.Bytes())
}

// BufferedObject returns the underlying reader or writer depending on the
// request. It returns a reader when request is a read request and a writer
// if request if a write request
func (f *FileBuffer) BufferedObject() any {
	if f.r != nil {
		return f.r
	}
	return f.w
}

// Close resources associated with buffered io operations
func (f *FileBuffer) Close() error {
	if f.w != nil {
		return f.w.Flush()
	}
	f.buf.Reset()
	return nil
}

package dit

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
)

//
type FileBuffer struct {
	// r/w is a buffered reader/writer. the underlying type
	// is a bufio.Reader/Writer depending on the opcode of
	// of the request
	r io.Reader
	w io.Writer

	buf *bytes.Buffer
}

// FileBufferFunc is any function that takes a path and returns a file
// buffer bound to the file the path points at.
//
// why doesn't this filebuffer thing use the request.Filename to open the file??
// TFTP servers are allowed to serve files from any directory(not only the root
// filesystem). So paths are relative to the directory from which files are
// being served from. The servers have more extensive and proper view of what
// the paths are supposed to mean so let them decide the path they want to use
// by providing this type which is just a function
type FileBufferFunc func(path string) (*FileBuffer, error)

// NewFileBufferFunc returns the request of the
func NewFileBufferFunc(req *ReadWriteRequest) (*ReadWriteRequest, FileBufferFunc) {
	var (
		f          *os.File
		err        error
		bufferFunc FileBufferFunc
		buf        = &FileBuffer{buf: new(bytes.Buffer)}
	)

	if op := req.opcode(); op == Rrq {
		// open file for reading
		bufferFunc = func(path string) (*FileBuffer, error) {
			if f, err = os.Open(path); err != nil {
				return nil, err
			}
			buf.r = bufio.NewReader(f)
			return buf, nil
		}
	} else if op == Wrq {
		// open and truncate file for writing
		bufferFunc = func(path string) (*FileBuffer, error) {
			if f, err = os.OpenFile(path,
				os.O_WRONLY|os.O_TRUNC|os.O_APPEND|os.O_CREATE, 0666); err != nil {
				return nil, err
			}
			buf.w = bufio.NewWriter(f)
			return buf, nil
		}
	}

	return req, bufferFunc
}

// Read tries to read exactly len(b) bytes into byte slice b. If the bytes
// left in the file at the end of read is not up to len(b) then it returns
// the io.EOF error to signal end of file.
func (f *FileBuffer) Read(b []byte) (int, error) {
	return io.ReadFull(f.r, b)
}

// ReadNext tries to returns the next set of len(b) bytes from the
// underlying data source. Keeping the bytes read in a temporary buffer
// incase there is the need to retransmit it
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
	// 3. read nothing and written nothing
	return read, err
}

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
// recent data read/written from/to underlying storage
func (f *FileBuffer) BufferLen() int {
	return f.buf.Len()
}

// ReadBuffer copies tries to copy len(b) bytes from the temporary buffer
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

// Write
//
// use only when handling write requests
func (f *FileBuffer) Write(b []byte) (int, error) {
	return f.w.Write(b)
}

// Close the bufio objects and flush the writer
func (f *FileBuffer) Close() error {
	if f.w != nil {
		return f.w.(*bufio.Writer).Flush()
	}
	return nil
}

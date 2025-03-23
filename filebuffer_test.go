package dit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

type mockFile struct {
	*bytes.Buffer
	closed bool
}

func newMockFile(content string) *mockFile {
	return &mockFile{Buffer: bytes.NewBuffer([]byte(content))}
}

func (m *mockFile) Close() error {
	m.closed = true
	return nil
}

func createTempFile(t *testing.T, content []byte) (*os.File, string) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.dat")
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("failed to create temp file %v", err)
	}

	if _, err := file.Write(content); err != nil {
		file.Close()
		t.Fatalf("failed to write content to test file %v", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		file.Close()
		t.Fatalf("failed to seek to beginning in test file %v", err)
	}
	return file, filePath
}

func TestBasicRead(t *testing.T) {
	content := "This is test content for reading"
	mockFile := newMockFile(content)

	buffer := NewFileBuffer()
	buffer.WithRequest(Rrq, mockFile)

	data := make([]byte, len(content))
	n, err := buffer.Read(data)

	if err != nil {
		t.Errorf("Read returned error: %v", err)
	}

	if n != len(content) {
		t.Errorf("Read returned incorrect byte count: got %d, want %d", n, len(content))
	}

	if string(data) != content {
		t.Errorf("Read returned incorrect data: got %s, want %s", string(data), content)
	}

	// Verify that Read doesn't update the retransmission buffer
	if buffer.BufferLen() != 0 {
		t.Error("Regular Read should not update the retransmission buffer")
	}
}

func TestRealFileRead(t *testing.T) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	file, _ := createTempFile(t, data)
	defer file.Close()

	buffer := NewFileBuffer()
	buffer.WithRequest(Rrq, file)
	defer buffer.Close()

	block := make([]byte, 512)
	n, err := buffer.ReadNext(block)
	if err != nil {
		t.Fatalf("ReadNext failed: %v", err)
	}
	if n != 512 {
		t.Errorf("Read incorrect number of bytes: got %d, want 512", n)
	}

	for i := 0; i < 10; i++ {
		if block[i] != byte(i%256) {
			t.Errorf("Data at position %d is incorrect: got %d, want %d",
				i, block[i], byte(i%256))
		}
	}

	if buffer.BufferLen() != 512 {
		t.Errorf("Buffer size incorrect: got %d, want 512", buffer.BufferLen())
	}
}

func TestBasicWrite(t *testing.T) {
	mockFile := newMockFile("")

	buffer := NewFileBuffer()
	buffer.WithRequest(Wrq, mockFile)

	content := "This is test content for writing"
	n, err := buffer.Write([]byte(content))

	if err != nil {
		t.Errorf("Write returned error: %v", err)
	}

	if n != len(content) {
		t.Errorf("Write returned incorrect byte count: got %d, want %d", n, len(content))
	}

	if buffer.w.Buffered() != len(content) {
		t.Errorf("Incorrect number of bytes buffered: got %d, want %d",
			buffer.w.Buffered(), len(content))
	}

	if buffer.BufferLen() != 0 {
		t.Error("Regular Write should not update the retransmission buffer")
	}

	buffer.Close()

	if mockFile.String() != content {
		t.Errorf("Written data mismatch: got %s, want %s", mockFile.String(), content)
	}
}

func TestRealFileWrite(t *testing.T) {
	file, path := createTempFile(t, nil)
	defer file.Close()

	buffer := NewFileBuffer()
	buffer.WithRequest(Wrq, file)

	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i % 256)
	}
	n, err := buffer.WriteNext(data)
	if err != nil {
		t.Fatalf("WriteNext failed: %v", err)
	}
	if n != 512 {
		t.Errorf("Wrote incorrect number of bytes: got %d, want 512", n)
	}
	buffer.Close()
	file.Close()

	// Read back the file and verify content
	readData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read file back: %v", err)
	}

	if len(readData) != 512 {
		t.Errorf("File has incorrect size: got %d, want 512", len(readData))
	}

	for i := 0; i < 10; i++ {
		if readData[i] != byte(i%256) {
			t.Errorf("File data at position %d is incorrect", i)
		}
	}
}

func TestWriteNext(t *testing.T) {
	mockFile := newMockFile("")

	buffer := NewFileBuffer()
	buffer.WithRequest(Wrq, mockFile)

	content := "This is test content for WriteNext"
	n, err := buffer.WriteNext([]byte(content))
	if err != nil {
		t.Errorf("WriteNext returned error: %v", err)
	}

	if n != len(content) {
		t.Errorf("WriteNext returned incorrect byte count: got %d, want %d", n, len(content))
	}
	if buffer.BufferLen() != len(content) {
		t.Errorf("Buffer length incorrect: got %d, want %d", buffer.BufferLen(), len(content))
	}

	bufferData := make([]byte, buffer.BufferLen())
	readLen := buffer.ReadBuffer(bufferData)
	if readLen != len(content) {
		t.Errorf("ReadBuffer returned incorrect byte count: got %d, want %d", readLen, len(content))
	}
	if string(bufferData) != content {
		t.Errorf("Buffer contains incorrect data: got %s, want %s", string(bufferData), content)
	}

	buffer.Close()

	if mockFile.String() != content {
		t.Errorf("Written data mismatch: got %s, want %s", mockFile.String(), content)
	}
}

func TestReset(t *testing.T) {
	content := "This is test content for reset testing"
	mockFile := newMockFile(content)

	buffer := NewFileBuffer()
	buffer.WithRequest(Rrq, mockFile)

	data := make([]byte, len(content))
	buffer.ReadNext(data)

	if buffer.BufferLen() == 0 {
		t.Fatalf("Buffer should have content before reset")
	}

	buffer.Reset()
	if buffer.BufferLen() != 0 {
		t.Errorf("Buffer should be empty after reset, but has %d bytes", buffer.BufferLen())
	}
}

func TestReadError(t *testing.T) {
	content := "Short content"
	mockFile := newMockFile(content)

	buffer := NewFileBuffer()
	buffer.WithRequest(Rrq, mockFile)

	// Try to read more than available
	data := make([]byte, len(content)+10)
	_, err := buffer.Read(data)

	if err == nil {
		t.Errorf("Expected error reading past EOF, got nil")
	}

	// Test ReadNext with error
	data = make([]byte, len(content)+10)
	_, err = buffer.ReadNext(data)

	if err == nil {
		t.Errorf("Expected error from ReadNext reading past EOF, got nil")
	}

	// Ensure the buffer doesn't contain invalid data after error
	if buffer.BufferLen() > len(content) {
		t.Errorf("Buffer should not contain more data than was available")
	}
}

func TestFileIdentification(t *testing.T) {
	file, path := createTempFile(t, []byte("test"))
	defer file.Close()

	buffer := NewFileBuffer()
	buffer.WithRequest(Rrq, file)

	if !buffer.Is(filepath.Base(path)) {
		t.Errorf("Is() failed to identify the correct file")
	}

	if buffer.Is("nonexistent.txt") {
		t.Errorf("Is() incorrectly matched a non-matching filename")
	}
}

func TestLargeFileReads(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large file test in short mode")
	}

	size := 5 * 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	t.Logf("data at position 1024: %d", data[1024])

	file, _ := createTempFile(t, data)
	defer file.Close()

	buffer := NewFileBuffer()
	buffer.WithRequest(Rrq, file)

	blockSize := 512
	block := make([]byte, blockSize)
	positions := []int{0, 1024, size / 2, size - blockSize}

	for i, pos := range positions {
		if _, err := file.Seek(int64(pos), 0); err != nil {
			t.Fatalf("failed to seek to position %d: %v", pos, err)
		}
		buffer.Reset()

		n, err := buffer.ReadNext(block)
		if err != nil {
			t.Fatalf("failed to read block at position %d: %v", pos, err)
		}
		if n != blockSize {
			t.Errorf("read incorrect block size, offset: %d, size %d", pos, n)
		}

		if block[0] != byte(pos%251) {
			t.Fatalf("incorrect data at pos %d, block %d, data %d", pos, i, block[0])
		}

		retransmitBlock := make([]byte, buffer.BufferLen())
		buffer.ReadBuffer(retransmitBlock)
		if data[pos] != retransmitBlock[0] {
			t.Fatalf("incorrect restransmission data at pos %d, block %d", pos, i)
		}

		if _, err := file.Seek(0, 0); err != nil {
			t.Fatalf("failed to seek to position %d: %v", pos, err)
		}
	}
}

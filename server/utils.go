package server

import (
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/Joe-Degs/dit"
)

var bufferPool = sync.Pool{
	New: func() interface{} { return dit.NewFileBuffer() },
}

func getBuffer(opcode dit.Opcode, file io.ReadWriteCloser) *dit.FileBuffer {
	buf := bufferPool.Get().(*dit.FileBuffer)
	if buf.Is(file) {
		buf.Reset()
		return buf
	}
	buf.Close()
	buf.WithRequest(opcode, file)
	return buf
}

func putBuffer(buf *dit.FileBuffer) { bufferPool.Put(buf) }

type newlogger struct {
	*log.Logger
}

func (l *newlogger) Info(format string, v ...any) {
	pre := l.Prefix()
	defer func() {
		l.SetPrefix(pre)
	}()
	l.SetPrefix(fmt.Sprintf("%s [INFO] ", pre))
	l.Printf(format, v...)
}

func (l *newlogger) Error() {
	return
}

func exitf(format string, v ...any) {
	log.Fatalf(fmt.Sprintf("dit: %s", format), v...)
}

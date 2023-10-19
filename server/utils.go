package server

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

var verbose bool

const (
	reset  = "\033[0m"
	ared   = "\033[31m"
	agreen = "\033[32m"
)

func dirExists(filename string) bool {
	fi, err := os.Stat(filename)
	return err != nil && fi.IsDir()
}

type logger struct {
	*log.Logger
	prefix   string
	writeErr bool
	out, err io.Writer
}

func newlogger(prefix string, out, err io.Writer) *logger {
	l := &logger{prefix: prefix, out: out, err: err}
	l.Logger = log.New(l, prefix, 0)
	return l
}

func (l *logger) Write(b []byte) (int, error) {
	t := time.Now().Format("2006-01-02 15-04-05.000000 ")
	if l.writeErr {
		return l.err.Write(append([]byte(t), b...))
	}
	return l.out.Write(append([]byte(t), b...))
}

func red(s string) string {
	return fmt.Sprintf("%s%s%s", ared, s, reset)
}

func green(s string) string {
	return fmt.Sprintf("%s%s%s", agreen, s, reset)
}

func (l *logger) Info(format string, v ...any) {
	pre := l.Prefix()
	defer func() {
		l.SetPrefix(pre)
	}()
	l.SetPrefix(fmt.Sprintf("[ %s ]  %s: ", green("INFO"), pre))
	l.Printf(format, v...)
}

func (l *logger) Error(format string, v ...any) {
	pre := l.Prefix()
	out := l.Writer()
	defer func() {
		l.SetPrefix(pre)
		l.SetOutput(out)
		l.writeErr = false
	}()
	l.writeErr = true
	l.SetPrefix(fmt.Sprintf("[ %s ] %s: ", red("ERROR"), pre))
	l.Printf(format, v...)
}

func (l *logger) Fatalf(format string, v ...any) {
	l.Error(format, v...)
	os.Exit(1)
}

func (l *logger) Verbose(format string, v ...any) {
	if verbose {
		l.Info(format, v...)
	}
}

func exitf(format string, v ...any) {
	log.Fatalf(fmt.Sprintf("dit: %s", format), v...)
}

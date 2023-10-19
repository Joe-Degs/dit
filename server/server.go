package server

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Joe-Degs/dit"
)

type server struct {
	*dit.Conn
	log        *logger
	opts       *Opts
	nextId     *atomic.Int64
	dir        string
	closed     chan bool
	connParams config

	// connection pool
	pool sync.Pool
}

// newServer returns a new tftp server
func newServer(opts *Opts) (*server, error) {
	conn, err := udpListen(opts.Address)
	if err != nil {
		return nil, err
	}
	verbose = opts.Verbose

	s := &server{
		Conn:       conn,
		opts:       opts,
		nextId:     &atomic.Int64{},
		log:        newlogger("ditserver", opts.Out, opts.Err),
		closed:     make(chan bool),
		dir:        opts.Secure,
		connParams: opts.connConfig(),
	}
	if dirExists(opts.Secure) {
		err := errors.New("failed to init sever: tftp directory does not exist")
		s.log.Error("%v", err)
		return nil, err
	}
	s.pool = sync.Pool{
		New: func() any {
			return newsrvconn(s.log, s.connParams)
		},
	}
	return s, nil
}

func (s *server) putconn(sconn *srvconn) {
	s.log.Info("just before segfault")
	sconn.buf.Reset() // reset buffer
	if sconn.f != nil {
		sconn.f.Seek(0, 0) // seek back to beginning of file
	}
	sconn.Conn.Close()
	s.pool.Put(sconn)
}

func (s *server) newconn(conn *dit.Conn) (*srvconn, error) {
	sconn := s.pool.Get().(*srvconn)
	sconn.Conn = conn
	req := conn.Request()
	filename := filepath.Join(s.dir, req.Filename)

	if sconn.buf.Is(filename) {
		return sconn, nil
	}

	var flags int
	switch req.Opcode {
	case dit.Rrq:
		flags = os.O_RDONLY
	case dit.Wrq:
		flags = os.O_WRONLY | os.O_TRUNC
		if sconn.cfg.Create {
			flags |= os.O_CREATE
		}
	}

	f, err := os.OpenFile(filename, flags, fs.ModePerm)
	if err != nil {
		var serr error
		switch {
		// TODO(Joe): this error handling is completely wrong. ex: if its
		// a new file but cfg says we can create, then we create it.
		case errors.Is(err, os.ErrExist):
			serr = sconn.WriteErr(dit.FileAlreadyExists, "file already exists")
		case errors.Is(err, os.ErrNotExist) && !sconn.cfg.Create:
			serr = sconn.WriteErr(dit.FileNotFound, "file does not exist")
		case errors.Is(err, os.ErrPermission):
			serr = sconn.WriteErr(dit.AccessViolation, "permision denied")
		default:
			serr = sconn.WriteErr(dit.NotDefined, "failed for reasons unknown")
		}
		if serr != nil {
			err = fmt.Errorf("%w: %w", err, serr)
		}

		// close the connection
		s.putconn(sconn)

		// return error to parent
		return nil, err
	}
	sconn.f = f
	sconn.buf.WithRequest(req.Opcode, f)
	return sconn, nil
}

func (s *server) start() error {
	cl := make(chan io.Closer)
	cc := make(chan *srvconn)

	go s.handleSignals(cl)
	s.log.Info("started and running on %s\n", s.Addr())

	go func() {
		for {
			conn, err := s.Accept()
			if err != nil {
				log.Fatal(err)
			}
			req := conn.Request()
			s.log.Verbose("recieved %s <file=%s mode=%s> from %s\n", req.Opcode, req.Filename, req.Mode, conn.Addr())

			sconn, err := s.newconn(conn)
			if err != nil {
				s.log.Error("failed to init new connection handler: %v\n", err)
				continue
			}
			go sconn.start(cc)
		}
	}()

	for {
		select {
		case <-s.closed:
			cl <- s
			close(cc)
			break
		case conn := <-cc:
			s.putconn(conn)
		}
	}

	return s.Close()
}

func (s *server) handleSignals(shutdownc <-chan io.Closer) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	for {
		sig := <-c
		s.log.Verbose("recieved a signal")
		sysSig, ok := sig.(syscall.Signal)
		if !ok {
			s.log.Fatal("not a unix signal")
		}
		switch sysSig {
		case syscall.SIGHUP:
			s.log.Info(`got "%v" signal: restarting server`, sig)
			if err := restartProcess(); err != nil {
				s.log.Fatalf("failed to restart process: %v", err)
			}
		case syscall.SIGINT, syscall.SIGTERM:
			s.log.Verbose(`handling termination (%v) signal`, sig)
			s.closed <- true
			s.log.Info(`got "%v" signal: shutting down`, sig)
			donec := make(chan bool)
			go func() {
				cl := <-shutdownc
				if err := cl.Close(); err != nil {
					s.log.Fatalf("error while shutting down: %v", err)
				}
				donec <- true
			}()
			select {
			case <-donec:
				s.log.Info("Goodbye!")
				os.Exit(0)
			case <-time.After(2 * time.Second):
				s.log.Fatal("timedout while trying to shutdown.")
			}
		default:
			s.log.Fatal("recieved another signal, should not happen.")
		}
	}
}

func Main(args []string, stdout io.Writer, stderr io.Writer) {
	options, getopt := NewOpts()
	if _, err := getopt.Parse(args); err != nil {
		exitf("failed to parse args: %v", err)
	}
	if getopt.Called("help") {
		exitf("%s\n", getopt.Help())
	}
	options.outputs(stdout, stderr)

	srv, err := newServer(options)
	if err != nil {
		exitf("failed to init server %v\n", err)
	}

	if err := srv.start(); err != nil {
		exitf("failed to start server %v\n", err)
	}
}

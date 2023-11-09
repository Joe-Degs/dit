package server

import (
	"fmt"
	"io"
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
	abs, err := filepath.Abs(opts.Secure)
	if err != nil {
		return nil, err
	}

	if !dirExists(abs) {
		return nil, fmt.Errorf("directory '%s' does not exist", opts.Secure)
	}

	verbose = opts.Verbose

	conn, err := udpListen(opts.Address)
	if err != nil {
		return nil, err
	}
	s := &server{
		Conn:       conn,
		opts:       opts,
		nextId:     &atomic.Int64{},
		log:        newlogger("ditserver", opts.Out, opts.Err),
		closed:     make(chan bool),
		dir:        abs,
		connParams: opts.connConfig(),
	}
	s.pool = sync.Pool{
		New: func() any {
			return newsrvconn(s.dir, s.log, s.connParams)
		},
	}
	return s, nil
}

func (s *server) newconn(conn *dit.Conn) (*srvconn, error) {
	sconn := s.pool.Get().(*srvconn)
	sconn.Conn = conn
	return sconn, nil
}

func (s *server) putconn(sconn *srvconn) {
	s.pool.Put(sconn)
}

func (s *server) start() error {
	cl := make(chan io.Closer)
	cc := make(chan *srvconn)

	go s.handleSignals(cl)
	s.log.Info("started and running <addr='%s' directory='%s'>", s.Addr(), s.dir)

	go func() {
		for {
			conn, err := s.Accept()
			if err != nil {
				log.Fatal(err)
			}
			req := conn.Request()
			s.log.Verbose("recieved %s <file=%s mode=%s> from %s\n", req.Opcode, req.Filename, req.Mode, conn.Addr())

			// get new connection from pool
			sconn, err := s.newconn(conn)
			if err != nil {
				s.log.Error("failed to init new connection handler: %v\n", err)
				conn.WriteErr(dit.NotDefined, "failed to create connection")
				continue
			}
			go sconn.start(cc)
		}
	}()

	for {
		select {
		case <-s.closed:
			close(cc)
			cl <- s
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

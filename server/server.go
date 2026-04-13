package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Joe-Degs/dit"
)

func isClosedNetworkError(err error) bool {
	// Check if it's a network closed error using proper error handling
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Err.Error() == "use of closed network connection"
	}
	return false
}

type server struct {
	*dit.Conn
	log        *logger
	opts       *Opts
	nextId     *atomic.Int64
	dir        string
	ctx        context.Context
	cancel     context.CancelFunc
	connParams config
	pidfile    *os.File

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

	connParams := opts.connConfig()
	conn, err := udpListen(opts.Address, connParams.Network)
	if err != nil {
		return nil, err
	}

	log := newlogger("ditserver", opts.Out, opts.Err)

	// Drop privileges after binding to the socket but before serving
	if opts.User != "" {
		log.Verbose("attempting to drop privileges to user '%s'", opts.User)
		if err := dropPrivileges(opts.User); err != nil {
			conn.Close()
			return nil, fmt.Errorf("privilege drop failed: %w", err)
		}
		log.Info("successfully dropped privileges to user '%s' (uid=%d, gid=%d)", opts.User, os.Getuid(), os.Getgid())
	}

	pidfile, err := createPidfile(opts.Pidfile)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if pidfile != nil {
		log.Info("created pidfile %s with PID %d", opts.Pidfile, os.Getpid())
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &server{
		Conn:       conn,
		opts:       opts,
		nextId:     &atomic.Int64{},
		log:        log,
		ctx:        ctx,
		cancel:     cancel,
		dir:        abs,
		connParams: connParams,
		pidfile:    pidfile,
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
	cc := make(chan *srvconn)

	go s.handleSignals()
	s.log.Info("started and running <addr='%s' directory='%s'>", s.Addr(), s.dir)

	go func() {
		defer close(cc)
		for {
			select {
			case <-s.ctx.Done():
				s.log.Verbose("accept loop shutting down")
				return
			default:
			}

			conn, err := s.AcceptRange(s.connParams.PortRangeLo, s.connParams.PortRangeHi)
			if err != nil {
				select {
				case <-s.ctx.Done():
					return
				default:
					if isClosedNetworkError(err) {
						s.log.Verbose("accept connection closed during shutdown")
						return
					}
					s.log.Error("accept error: %v", err)
					return
				}
			}
			req := conn.Request()
			s.log.Verbose("recieved %s <file=%s mode=%s> from %s\n", req.Opcode, req.Filename, req.Mode, conn.Addr())

			sconn, err := s.newconn(conn)
			if err != nil {
				s.log.Error("failed to init new connection handler: %v\n", err)
				conn.WriteErr(dit.NotDefined, "failed to create connection")
				continue
			}
			go sconn.start(s.ctx, cc)
		}
	}()

	for {
		select {
		case <-s.ctx.Done():
			s.log.Verbose("server context cancelled, shutting down")
			return s.Close()
		case conn := <-cc:
			if conn != nil {
				s.putconn(conn)
			}
		}
	}
}

func (s *server) handleSignals() {
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
			s.log.Info(`got "%v" signal: shutting down`, sig)

			// Cancel context to trigger graceful shutdown
			s.cancel()

			// Give a moment for graceful shutdown
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			done := make(chan struct{})
			go func() {
				<-s.ctx.Done()
				removePidfile(s.pidfile, s.opts.Pidfile)
				close(done)
			}()

			select {
			case <-done:
				s.log.Info("Goodbye!")
				os.Exit(0)
			case <-shutdownCtx.Done():
				s.log.Fatal("timedout while trying to shutdown.")
			}
		default:
			s.log.Fatal("recieved another signal, should not happen.")
		}
	}
}

func Main(args []string, stdout io.Writer, stderr io.Writer) {
	MainWithVersion(args, stdout, stderr, "dev", "unknown", "unknown")
}

func MainWithVersion(args []string, stdout io.Writer, stderr io.Writer, version, gitCommit, buildTime string) {
	options, getopt := NewOpts()
	if _, err := getopt.Parse(args); err != nil {
		exitf("failed to parse args: %v", err)
	}
	if getopt.Called("help") {
		exitf("%s\n", getopt.Help())
	}
	if getopt.Called("version") {
		fmt.Fprintf(stdout, "tftpd version %s\n", version)
		fmt.Fprintf(stdout, "Git commit: %s\n", gitCommit)
		fmt.Fprintf(stdout, "Build time: %s\n", buildTime)
		os.Exit(0)
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

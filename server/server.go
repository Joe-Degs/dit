package server

import (
	"io"
	"log"

	"github.com/Joe-Degs/dit"
)

type server struct {
	*dit.Conn
	log  *newlogger
	opts *Opts
}

// newServer returns a new tftp server
func newServer(opts *Opts) (*server, error) {
	conn, err := udpListen(opts.Address)
	if err != nil {
		return nil, err
	}

	return &server{
		Conn: conn,
		opts: opts,
		log:  &newlogger{log.New(opts.Out, "dit", log.LstdFlags)},
	}, nil
}

func (s *server) start() error {
	s.log.Info("server started and running on %s\n", s.Addr())
	for {
		conn, err := s.Accept()
		if err != nil {
			log.Fatal(err)
		}
		switch req := conn.Request(); req.Opcode {
		case dit.Rrq:
			s.log.Info("%+v\n", req)
		case dit.Wrq:
			s.log.Info("%+v\n", req)
		}
		conn.Close()
	}
}

func Main(args []string, stdout io.Writer, stderr io.Writer) {
	options, getopt := NewOpts()
	_, err := getopt.Parse(args)
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

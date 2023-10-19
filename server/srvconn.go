package server

import (
	"os"

	"github.com/Joe-Degs/dit"
)

type srvconn struct {
	*dit.Conn
	id  int64
	log *logger
	cfg config
	buf *dit.FileBuffer
	f   *os.File
}

func newsrvconn(log *logger, cfg config) *srvconn {
	return &srvconn{
		cfg: cfg,
		log: log,
		buf: dit.NewFileBuffer(),
	}
}

func (s *srvconn) start(cl chan<- *srvconn) {
	req := s.Request()

	switch req.Opcode {
	case dit.Rrq:
		s.log.Info("%+v\n", req)
	case dit.Wrq:
		s.log.Info("%+v\n", req)
	}

	cl <- s
}

func (s *srvconn) Close() (err error) {
	if s.f != nil {
		err = s.f.Close()
	}
	if err1 := s.Conn.Close(); err1 != nil {
		err = err1
	}
	return
}

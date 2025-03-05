package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Joe-Degs/dit"
)

type srvconn struct {
	*dit.Conn
	id  int64
	dir string
	log *logger
	cfg config
	buf *dit.FileBuffer
	f   *os.File
}

func newsrvconn(dir string, log *logger, cfg config) *srvconn {
	return &srvconn{
		cfg: cfg,
		log: log,
		dir: dir,
		buf: dit.NewFileBuffer(),
	}
}

func (s *srvconn) init() error {
	req := s.Request()
	filename := filepath.Join(s.dir, req.Filename)

	if s.buf.Is(filename) {
		return nil
	}

	// stat and file info stuff before open now
	_, err := os.Stat(filename)
	if err != nil {
		s.log.Error("stat error: %+v", err)
		var serr error
		switch {
		case errors.Is(err, os.ErrNotExist) && !s.cfg.Create:
			serr = s.WriteErr(dit.FileNotFound, "file does not exist")
		case errors.Is(err, os.ErrPermission):
			serr = s.WriteErr(dit.AccessViolation, "permision denied")
		default:
			serr = s.WriteErr(dit.NotDefined, "could not stat file")
		}

		if serr != nil {
			err = fmt.Errorf("%w: failed to send error: %w", err, serr)
		}

		return err
	}

	var flags int
	switch req.Opcode {
	case dit.Rrq:
		flags = os.O_RDONLY
	case dit.Wrq:
		flags = os.O_WRONLY | os.O_TRUNC
		if s.cfg.Create {
			flags |= os.O_CREATE
		}
	}

	f, err := os.OpenFile(filename, flags, fs.ModePerm)
	if err != nil {
		s.log.Error("open error: %+v", err)
		if e := s.WriteErr(dit.NotDefined, "could not stat file"); e != nil {
			return fmt.Errorf("%w: could not send error packet %w", err, e)
		}
		return err
	}

	s.f = f
	s.buf.WithRequest(req.Opcode, f)
	return nil
}

func (s *srvconn) start(cl chan<- *srvconn) {
	if err := s.init(); err != nil {
		s.Close()
		s.log.Error("failed to initialize connection: %v", err)
		return
	}

	req := s.Request()

	switch req.Opcode {
	case dit.Rrq:
		s.log.Info("%+v\n", req)
	case dit.Wrq:
		s.log.Info("%+v\n", req)
	}

	cl <- s
}

func (s *srvconn) end() *srvconn {
	s.buf.Reset() // reset buffer
	if s.f != nil {
		s.f.Seek(0, 0) // seek back to beginning of file
	}
	s.Conn.Close()
	return s
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

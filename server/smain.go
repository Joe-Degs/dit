package server

import (
	"fmt"
	"io"
	"os"

	"github.com/Joe-Degs/dit/internal/config"
)

func Main(args []string, stdout io.Writer, stderr io.Writer) error {
	_, opt := config.NewOpts()
	_, err := opt.Parse(args)
	if opt.Called("help") {
		fmt.Fprintln(stderr, opt.Help())
		os.Exit(1)
	}
	return err
}

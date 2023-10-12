package main

import (
	"os"

	"github.com/Joe-Degs/dit/server"
)

func main() {
	server.Main(os.Args[1:], os.Stdout, os.Stderr)
}

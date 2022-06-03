package main

import (
	"log"
	"os"

	"github.com/Joe-Degs/dit/server"
)

func main() {
	if err := server.Main(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		log.Fatal(err)
	}
}

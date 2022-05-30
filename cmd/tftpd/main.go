package main

import "log"

func main() {
	srv, err := newServer("udp6", ":69")
	if err != nil {
		log.Fatal(err)
	}
	srv.start()
}

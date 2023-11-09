package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"runtime/pprof"
	"strings"

	"github.com/Joe-Degs/dit/server"
)

var (
	// -profile cpu, mem
	profile = flag.String("profile", "", "run go pprof")
)

func init() {
	flag.CommandLine.Init(os.Args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	flag.CommandLine.Usage = func() {}
}

func main() {
	flag.Parse()
	var stop func()
	if *profile != "" {
		stop = doProfile(*profile)
	}
	if stop != nil {
		defer stop()
	}

	for i, str := range os.Args {
		if strings.Contains(str, "profile") {
			os.Args = append(os.Args[:i], os.Args[i+2:]...)
			break
		}
	}

	server.Main(os.Args[1:], os.Stdout, os.Stderr)
}

func doProfile(typ string) func() {
	var pprofer func(io.Writer) error
	var stop func()
	switch typ {
	case "cpu":
		pprofer = pprof.StartCPUProfile
		stop = func() { pprof.StopCPUProfile() }
	default:
		profiler := pprof.Lookup(typ)
		if profiler == nil {
			return nil
		}
		pprofer = func(w io.Writer) error { return profiler.WriteTo(w, 0) }
		stop = func() { return }
	}
	if pprofer != nil {
		f, err := os.OpenFile(
			fmt.Sprintf("bin/%s.out", typ),
			os.O_CREATE|os.O_TRUNC|os.O_RDWR,
			fs.ModePerm,
		)
		if err != nil {
			log.Fatal(err)
		}
		pprofer(f)
		return func() {
			stop()
			f.Close()
		}
		log.Printf("%s profiler started", typ)
	}
	return stop
}

package server

import (
	"io"

	"github.com/DavidGamba/go-getoptions"
)

// Opts are tftpd compatible flags to configure the behaviour of the server
type Opts struct {
	Address   string // --address|-a [address][:port]
	PortRange string // --port-range|-R port:port
	Secure    string // --secure|-s path/to/dir
	User      string // --user|-u usename
	Pidfile   string // --pidfile|-p pidfile
	Verbosity string // --verbosity value
	Refuse    string // --refuse|-r tftp-option

	BlockSize  int // --blocksize|-B max-block-size
	Timeout    int // --timeout|-t secs
	Retransmit int // --restransmit|-T secs

	IPv4       bool // --ipv6|-4
	IPv6       bool // --ipv4|-6
	Listen     bool // --listen|-l
	Foreground bool // --foreground|-L
	Permissive bool // --permissive|-p
	Create     bool // --create|-c
	Verbose    bool // --verbose|-v
	Version    bool // --version|-V

	Out, Err io.Writer
}

func NewOpts() (*Opts, *getoptions.GetOpt) {
	var opts Opts
	opt := getoptions.New()

	// bundle short options together e.g: -4l
	opt.SetMode(getoptions.Bundling)

	opt.Bool("help", false, opt.Alias("h", "?"))

	// options accepting string values
	opt.StringVar(&opts.Address, "address", ":69", opt.Alias("a"), opt.Description("specify specific address and port to listen to when called with --listen or --foreground. the default is to listen on the tftp port specified in /etc/services on all local interfaces"))
	opt.StringVar(&opts.PortRange, "port-range", "", opt.Alias("R"), opt.Description("Force the designated server port number (TID) to be in specififed range"))
	opt.StringVar(&opts.Secure, "secure", "", opt.Alias("s"), opt.Description("Change the root sdirectory at server startup and serve/write files only fromt this directory. All paths are relative to the specified directory"))
	opt.StringVar(&opts.User, "user", "nobody", opt.Alias("u"), opt.Description("specify the username which the server will run as; the default is \"nobody\""))
	opt.StringVar(&opts.Pidfile, "pidfile", "", opt.Alias("P"), opt.Description("Write the process id of server to pidfile. Delete said pidfile during normal termination (SIGINT, SIGTERM)"))
	opt.StringVar(&opts.Verbosity, "verbosity", "", opt.Description("Set the verbosity level"))
	opt.StringVar(&opts.Refuse, "refuse", "", opt.Alias("r"), opt.Description("Specify which TFTP option from rfc2347 should be ignored"))

	// options accepting integer values
	opt.IntVar(&opts.BlockSize, "blocksize", 0, opt.Alias("B"), opt.Description("specify the maximum permitted block size. values in the range 512-65464 inclusive are permitted. a reasonable value is MTU - 32"))
	opt.IntVar(&opts.Timeout, "timeout", 900, opt.Alias("t"), opt.Description("Specify how long , in seconds to wait for a second request before terminating the connection"))
	opt.IntVar(&opts.Retransmit, "retransmit", 1000000, opt.Alias("T"), opt.Description("Determine the default timeout in microseconds before the first packet is retransmitted. It can be modified by the client during option negotiation"))

	// boolean options
	opt.BoolVar(&opts.IPv4, "ipv4", false, opt.Alias("4"), opt.Description("Connect with ipv4 only"))
	opt.BoolVar(&opts.IPv6, "ipv6", false, opt.Alias("6"), opt.Description("Connect with ipv6 only"))
	opt.BoolVar(&opts.Listen, "listen", false, opt.Alias("l"), opt.Description("Run the server in standalone mode, rather than from inetd"))
	opt.BoolVar(&opts.Foreground, "foreground", false, opt.Alias("L"), opt.Description("Same as --listen but do not detach process from foreground"))
	opt.BoolVar(&opts.Permissive, "permissive", false, opt.Alias("p"), opt.Description("perform no additional permission checks above the normal system-provided access controls from the user specified via the --user option"))
	opt.BoolVar(&opts.Create, "create", false, opt.Alias("c"), opt.Description("Allow new files to be created. By default, the server only allows for existing files to be updated"))
	opt.BoolVar(&opts.Verbose, "verbose", false, opt.Alias("v"), opt.Description("Verbose output"))
	opt.BoolVar(&opts.Version, "version", false, opt.Alias("V"), opt.Description("Print out version of server and exit"))

	return &opts, opt
}

func (o *Opts) outputs(out, err io.Writer) {
	o.Out = out
	o.Err = err
}

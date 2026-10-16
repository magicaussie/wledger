// wled-simulator serves an in-memory WLED subset on a literal loopback address.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/tuxedocurly/wledger/internal/wledsim"
)

// Literal addresses avoid hostname resolution and wildcard/dual-stack exposure.
func loopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("bind address must be IP:port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("bind address must use a literal loopback IP")
	}
	return nil
}

func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("wled-simulator", flag.ContinueOnError)
	flags.SetOutput(stderr)
	address := flags.String("bind", "127.0.0.1:8181", "literal loopback IP:port")
	count := flags.Int("count", 200, "logical RGB LED count")
	mac := flags.String("mac", "02:00:00:00:00:01", "stable unique unicast MAC (choose a different value per simulator)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if err := loopbackAddress(*address); err != nil {
		return err
	}
	simulator, err := wledsim.New(wledsim.Config{Count: *count, MAC: *mac})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return err
	}
	defer listener.Close()
	fmt.Fprintf(stderr, "WLED simulator listening on http://%s (no hardware), MAC %s\n", listener.Addr(), *mac)
	server := &http.Server{Handler: simulator, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 1 << 16}
	return server.Serve(listener)
}

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

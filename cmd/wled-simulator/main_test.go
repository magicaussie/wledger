package main

import (
	"io"
	"testing"
)

func TestLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8181", "127.0.0.2:0", "[::1]:8181"} {
		if err := loopbackAddress(address); err != nil {
			t.Errorf("%s: %v", address, err)
		}
	}
	for _, address := range []string{":8181", "0.0.0.0:8181", "[::]:8181", "192.0.2.1:8181", "localhost:8181", "example.com:8181", "::1"} {
		if err := loopbackAddress(address); err == nil {
			t.Errorf("accepted %s", address)
		}
	}
}

func TestRunRejectsUnsafeOrInvalidConfigBeforeListen(t *testing.T) {
	for _, args := range [][]string{{"-bind", "0.0.0.0:8181"}, {"-count", "0"}, {"-mac", "ff:ff:ff:ff:ff:ff"}, {"unexpected"}} {
		if err := run(args, io.Discard); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

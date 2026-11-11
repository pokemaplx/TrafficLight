// Command protoupdate refreshes the protos Traffic Light is built with.
//
//	go run ./cmd/protoupdate                       the newest ones there are
//	go run ./cmd/protoupdate path/to/pogo_pb2.py   or a file, or another url
//
// It takes what protoc writes for Python (pogo_pb2.py), or a descriptor set compiled with
// `protoc --descriptor_set_out`, and writes internal/protos/pogo.desc. Nothing is generated from
// it: Traffic Light reads the descriptors at startup.
//
// A running Traffic Light looks for new protos by itself (see protos_url), so this is only
// about what a build starts out with.
package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pokemaplx/TrafficLight/internal/protos"
)

const output = "internal/protos/pogo.desc"

func main() {
	from := protos.UpstreamProtos
	switch len(os.Args) {
	case 1:
	case 2:
		from = os.Args[1]
	default:
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/protoupdate [pogo_pb2.py | descriptor set | url]")
		os.Exit(2)
	}
	if err := run(from); err != nil {
		fmt.Fprintln(os.Stderr, "protoupdate:", err)
		os.Exit(1)
	}
}

func run(from string) error {
	source, err := read(from)
	if err != nil {
		return err
	}
	descriptor, err := protos.DescriptorFrom(source)
	if err != nil {
		return err
	}

	// the same checks Traffic Light does at startup, so a broken file never gets written
	registry, err := protos.Load(descriptor)
	if err != nil {
		return fmt.Errorf("%s doesn't hold usable protos: %w", from, err)
	}
	before, err := protos.Load(protos.Embedded())
	if err != nil {
		return err
	}

	if err := os.WriteFile(output, descriptor, 0o644); err != nil {
		return fmt.Errorf("%w (run this from the repository root)", err)
	}
	fmt.Printf("wrote %s from %s: %d messages and %d methods, there were %d and %d\n",
		output, from, registry.Messages(), registry.Methods(), before.Messages(), before.Methods())
	return nil
}

func read(from string) ([]byte, error) {
	if !strings.HasPrefix(from, "http://") && !strings.HasPrefix(from, "https://") {
		return os.ReadFile(from)
	}

	client := &http.Client{Timeout: time.Minute}
	response, err := client.Get(from)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New(from + " answered " + response.Status)
	}
	return io.ReadAll(io.LimitReader(response.Body, 64<<20))
}

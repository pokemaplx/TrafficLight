// Command protoupdate refreshes the proto definitions Traffic Light decodes with.
//
//	go run ./cmd/protoupdate path/to/pogo_pb2.py
//
// It takes what POGOProtos ships for Python (pogo_pb2.py), or a descriptor set compiled with
// `protoc --descriptor_set_out`, and writes internal/pogo/pogo.desc. Nothing is generated from
// it: Traffic Light reads the descriptors at startup, so updating protos is replacing that file.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/pokemaplx/TrafficLight/internal/pogo"
)

const output = "internal/pogo/pogo.desc"

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/protoupdate <pogo_pb2.py | descriptor set>")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "protoupdate:", err)
		os.Exit(1)
	}
}

func run(path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	descriptor, err := fileDescriptor(source)
	if err != nil {
		return err
	}

	// the same checks Traffic Light does at startup, so a broken file never gets written
	registry, err := pogo.Load(descriptor)
	if err != nil {
		return fmt.Errorf("%s doesn't hold usable protos: %w", path, err)
	}

	if err := os.WriteFile(output, descriptor, 0o644); err != nil {
		return fmt.Errorf("%w (run this from the repository root)", err)
	}
	fmt.Printf("wrote %s: %d messages, %d methods\n", output, registry.Messages(), registry.Methods())
	return nil
}

// fileDescriptor returns a serialized FileDescriptorProto, whatever the source is
func fileDescriptor(source []byte) ([]byte, error) {
	if marker := []byte("AddSerializedFile("); bytes.Contains(source, marker) {
		return pythonBytes(source[bytes.Index(source, marker)+len(marker):])
	}

	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(source, set); err == nil && len(set.File) > 0 && set.File[0].GetName() != "" {
		if len(set.File) > 1 {
			return nil, fmt.Errorf("the descriptor set holds %d files, Traffic Light expects a single one", len(set.File))
		}
		return proto.Marshal(set.File[0])
	}

	file := &descriptorpb.FileDescriptorProto{}
	if err := proto.Unmarshal(source, file); err == nil && file.GetName() != "" {
		return source, nil
	}
	return nil, errors.New("this is neither a pogo_pb2.py nor a compiled descriptor")
}

// pythonBytes reads the bytes literal(s) a pb2 file passes to AddSerializedFile, up to the
// closing parenthesis. Adjacent literals are joined, like Python does.
func pythonBytes(source []byte) ([]byte, error) {
	var out []byte
	i := 0
	for {
		for i < len(source) && (source[i] == ' ' || source[i] == '\t' || source[i] == '\r' || source[i] == '\n') {
			i++
		}
		if i >= len(source) {
			return nil, errors.New("the bytes literal never ends")
		}
		if source[i] == ')' {
			if out == nil {
				return nil, errors.New("AddSerializedFile got no bytes literal")
			}
			return out, nil
		}
		if source[i] != 'b' || i+1 >= len(source) || (source[i+1] != '\'' && source[i+1] != '"') {
			return nil, fmt.Errorf("expected a bytes literal, found %q", source[i])
		}

		quote := source[i+1]
		i += 2
		for {
			if i >= len(source) {
				return nil, errors.New("the bytes literal never ends")
			}
			c := source[i]
			i++
			if c == quote {
				break
			}
			if c != '\\' {
				out = append(out, c)
				continue
			}
			if i >= len(source) {
				return nil, errors.New("the bytes literal ends in a backslash")
			}

			c = source[i]
			i++
			switch c {
			case '\n':
				// a line continuation
			case '\\', '\'', '"':
				out = append(out, c)
			case 'a':
				out = append(out, 7)
			case 'b':
				out = append(out, 8)
			case 'f':
				out = append(out, 12)
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'v':
				out = append(out, 11)
			case 'x':
				if i+2 > len(source) || !isHex(source[i]) || !isHex(source[i+1]) {
					return nil, errors.New(`a \x escape needs two hex digits`)
				}
				out = append(out, hexValue(source[i])<<4|hexValue(source[i+1]))
				i += 2
			default:
				if c < '0' || c > '7' {
					// Python keeps the backslash of escapes it doesn't know
					out = append(out, '\\', c)
					continue
				}
				value := int(c - '0')
				for digits := 1; digits < 3 && i < len(source) && source[i] >= '0' && source[i] <= '7'; digits++ {
					value = value<<3 | int(source[i]-'0')
					i++
				}
				out = append(out, byte(value))
			}
		}
	}
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexValue(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

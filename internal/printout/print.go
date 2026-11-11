// Package printout is the "print" output: a plain log of every request on stdout
package printout

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pokemaplx/TrafficLight/internal/pogo"
)

type Printer struct {
	mu     sync.Mutex
	out    *bufio.Writer
	buffer []byte
}

func New(out io.Writer) *Printer {
	return &Printer{out: bufio.NewWriterSize(out, 64<<10)}
}

// Add prints one rpc and everything it carried
func (p *Printer) Add(request pogo.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()

	text := p.buffer[:0]
	text = time.Now().AppendFormat(text, "2006-01-02 15:04:05.000000")
	text = append(text, " | RPC ID "...)
	text = strconv.AppendInt(text, request.RPCID, 10)
	text = append(text, " | RPC STATUS "...)
	text = strconv.AppendInt(text, request.RPCStatus, 10)
	if request.RPCHandle != nil {
		text = append(text, " | RPC HANDLE "...)
		text = strconv.AppendInt(text, *request.RPCHandle, 10)
	}
	text = append(text, '\n', '\n')

	registry := pogo.Default()
	for _, raw := range request.Protos {
		text = appendProto(text, registry.Decode(raw), 1)
		text = append(text, '\n')
	}
	text = append(text, strings.Repeat("-", 100)...)
	text = append(text, '\n', '\n')

	_, _ = p.out.Write(text)
	_ = p.out.Flush()
	// one huge request shouldn't keep its buffer around forever
	if cap(text) <= 1<<20 {
		p.buffer = text
	} else {
		p.buffer = nil
	}
}

func appendProto(text []byte, proto *pogo.Proto, indent int) []byte {
	text = strconv.AppendInt(text, proto.MethodValue, 10)
	text = append(text, " | "...)
	text = append(text, methodName(proto)...)
	text = append(text, '\n')

	text = appendMessage(text, "Request", &proto.Request, indent)
	text = appendMessage(text, "Response", &proto.Response, indent)

	if proto.Proxy != nil {
		text = append(text, '\n')
		text = appendTabs(text, indent)
		text = append(text, "Proxy: "...)
		text = appendProto(text, proto.Proxy, indent+1)
	}
	return text
}

func appendMessage(text []byte, kind string, message *pogo.Message, indent int) []byte {
	text = appendTabs(text, indent)

	name := kind + " | " + message.Name
	if message.Name == "" {
		name = "[Unknown " + kind + " Message]"
	}
	text = append(text, name...)
	for padding := 50 - len(name); padding > 0; padding-- {
		text = append(text, ' ')
	}

	switch message.State() {
	case "unknown":
		text = append(text, "Blackbox: "...)
		text = message.AppendText(text, true)
	case "error":
		text = append(text, "Error decoding message. Blackbox: "...)
		text = message.AppendText(text, true)
	case "empty":
		text = append(text, "{}"...)
	default:
		text = message.AppendText(text, true)
	}
	return append(text, '\n')
}

func appendTabs(text []byte, count int) []byte {
	for ; count > 0; count-- {
		text = append(text, '\t')
	}
	return text
}

func methodName(proto *pogo.Proto) string {
	if proto.MethodName == "" {
		return "UNKNOWN"
	}
	return proto.MethodName
}

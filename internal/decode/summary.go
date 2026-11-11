package decode

import (
	"strconv"

	"github.com/pokemaplx/TrafficLight/internal/protos"
	"github.com/pokemaplx/TrafficLight/internal/traffic"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// AppendSummary appends everything the browser needs to list, filter and search a proto.
// Inspecting it loads AppendDetail.
//
// This runs for every record of the log whenever a browser connects, so it doesn't decode
// anything: the walker writes the text straight from the bytes (see wire.go).
func AppendSummary(dst []byte, registry *protos.Registry, raw traffic.RawProto) []byte {
	dst, _ = appendSummary(dst, registry, raw, 0)
	return dst
}

// status is a response's top level enum that tells whether a call worked
type status struct {
	field  protoreflect.FieldDescriptor
	number protoreflect.EnumNumber
}

// glance is what a summary says about a message
type glance struct {
	name  string
	state string
	size  int
	// the one line text, in the walker's scratch space
	text []byte
}

func appendSummary(dst []byte, registry *protos.Registry, raw traffic.RawProto, depth int) ([]byte, status) {
	name, value := registry.Resolve(raw.Method)
	requestType, responseType := registry.Types(value)

	w := getWalker()
	defer putWalker(w)
	request := w.glance(requestType, raw.Request)
	response := w.glance(responseType, raw.Response)

	// a proxy request carries another call inside of it
	proxyRequest, proxyResponse := registry.Proxy()
	proxied := depth < maxProxyDepth && proxyRequest != nil &&
		requestType == proxyRequest && responseType == proxyResponse &&
		request.decoded() && response.decoded()

	dst = append(dst, `{"method":`...)
	dst = appendName(dst, name)
	dst = append(dst, `,"value":`...)
	dst = strconv.AppendInt(dst, value, 10)
	// a proxy's own payload is just the proxied proto again, no need to search it twice
	dst = append(dst, `,"request":`...)
	dst = request.append(dst, !proxied)
	dst = append(dst, `,"response":`...)
	dst = response.append(dst, !proxied)

	// the response's status or result, i.e. ENCOUNTER_SUCCESS. A proxied one takes precedence
	shown := responseStatus(responseType, raw.Response, response.state)
	own := shown
	dst = append(dst, `,"proxy":`...)
	if proxied {
		var inner status
		dst, inner = appendSummary(dst, registry, traffic.RawProto{
			Method:   int64(uint32(lastVarint(raw.Request, proxyRequest.Fields().ByName("action").Number()))),
			Request:  lastBytes(raw.Request, proxyRequest.Fields().ByName("payload").Number()),
			Response: lastBytes(raw.Response, proxyResponse.Fields().ByName("payload").Number()),
		}, depth+1)
		if inner.field != nil {
			shown = inner
		}
	} else {
		dst = append(dst, "null"...)
	}

	dst = append(dst, `,"status":`...)
	if shown.field != nil {
		dst = append(dst, '"')
		dst = appendEnum(dst, shown.field, shown.number)
		dst = append(dst, '"')
	} else {
		dst = append(dst, "null"...)
	}
	return append(dst, '}'), own
}

// glance looks at a message: how it decodes, and its text
func (w *walker) glance(descriptor protoreflect.MessageDescriptor, raw []byte) glance {
	seen := glance{size: len(raw)}
	start := len(w.out)

	if descriptor == nil {
		seen.state = "unknown"
		w.out = appendBlackboxText(w.out, raw, true)
		seen.text = w.out[start:]
		return seen
	}
	seen.name = string(descriptor.Name())

	empty, err := w.text(descriptor, raw)
	if err == errUnsupported {
		// something only the decoder gets right
		message := decodeMessage(descriptor, raw)
		if message.Payload == nil {
			err = errMalformed
		} else {
			err = nil
			empty = isEmpty(message.Payload)
			w.out = AppendText(w.out, message.Payload, true)
		}
	}

	switch {
	case err != nil:
		seen.state = "error"
		w.out = appendBlackboxText(w.out[:start], raw, true)
	case empty:
		seen.state = "empty"
	default:
		seen.state = "ok"
	}
	seen.text = w.out[start:]
	return seen
}

func (g *glance) decoded() bool {
	return g.state == "ok" || g.state == "empty"
}

func (g *glance) append(dst []byte, withText bool) []byte {
	dst = append(dst, `{"name":`...)
	dst = appendName(dst, g.name)
	dst = append(dst, `,"state":"`...)
	dst = append(dst, g.state...)
	dst = append(dst, `","size":`...)
	dst = strconv.AppendInt(dst, int64(g.size), 10)
	dst = append(dst, `,"text":`...)
	if withText && g.state != "empty" {
		dst = AppendJSONBytes(dst, g.text)
	} else {
		dst = append(dst, '"', '"')
	}
	return append(dst, '}')
}

// responseStatus reads a response's status or result straight from its bytes
func responseStatus(descriptor protoreflect.MessageDescriptor, raw []byte, state string) status {
	if state != "ok" {
		return status{}
	}
	for _, name := range statusFields {
		field := descriptor.Fields().ByName(name)
		if field == nil || field.Enum() == nil || field.Cardinality() == protoreflect.Repeated {
			continue
		}
		return status{field: field, number: protoreflect.EnumNumber(int32(lastVarint(raw, protowire.Number(field.Number()))))}
	}
	return status{}
}

// Package receiver takes the traffic mitms post and hands it on, still encoded.
//
// Nothing is decoded here: a request is parsed just far enough to know its protos' bytes.
// Making sense of them is left to whoever looks at them, so traffic nobody is watching costs
// next to nothing.
package receiver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/pokemaplx/TrafficLight/internal/pogo"
)

// MaxBody is the biggest request the receiver reads
const MaxBody = 64 << 20

type Receiver struct {
	// Forward gets every body untouched, so the other end sees exactly what the mitm sent.
	// Whether Traffic Light itself can make sense of it is none of its business
	Forward func(body []byte)
	// Sink gets every rpc. The protos' bytes are its to keep
	Sink func(request pogo.Request)

	bodies sync.Pool
}

func (r *Receiver) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(w, request)
		return
	}
	if request.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	buffer := r.bodies.Get()
	body, err := readBody(w, request, buffer)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "couldn't read the request", http.StatusBadRequest)
		}
		return
	}

	requests, err := Parse(body)
	if err != nil && !json.Valid(body) {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if r.Forward != nil {
		// anything that is json goes on, even if Traffic Light itself can't make sense of it.
		// The body is the forwarder's from here on
		r.Forward(body)
	} else if cap(body) <= maxPooledBody {
		// nothing of a parsed request points into the body, so the next one can reuse it
		r.bodies.Put(&body)
	}
	if err != nil {
		http.Error(w, "malformed data: "+err.Error(), http.StatusBadRequest)
		return
	}
	for _, parsed := range requests {
		r.Sink(parsed)
	}
	_, _ = io.WriteString(w, "OK")
}

// bodies bigger than this aren't worth holding on to between requests
const maxPooledBody = 4 << 20

func readBody(w http.ResponseWriter, request *http.Request, pooled any) ([]byte, error) {
	reader := http.MaxBytesReader(w, request.Body, MaxBody)
	length := request.ContentLength
	if length < 0 || length > MaxBody {
		return io.ReadAll(reader)
	}

	// the length is known, so the body is read into a buffer of its size
	var body []byte
	if buffer, ok := pooled.(*[]byte); ok && int64(cap(*buffer)) >= length {
		body = (*buffer)[:length]
	} else {
		body = make([]byte, length)
	}
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

// what mitms send. Some call the protos "contents", their method "type" and their response
// "payload", and all of them are taken
type envelope struct {
	RPCID     *integer `json:"rpcid"`
	RPCStatus *integer `json:"rpcstatus"`
	RPCHandle *integer `json:"rpchandle"`
	Contents  *[]entry `json:"contents"`
	Protos    *[]entry `json:"protos"`
}

type entry struct {
	Type     *integer `json:"type"`
	Method   *integer `json:"method"`
	Request  *string  `json:"request"`
	Payload  *string  `json:"payload"`
	Response *string  `json:"response"`
}

// integer takes what mitms make of a number: 5, 5.0, "5" or true
type integer int64

func (i *integer) UnmarshalJSON(data []byte) error {
	text := string(data)
	switch {
	case text == "true":
		*i = 1
		return nil
	case text == "false":
		*i = 0
		return nil
	case len(text) > 1 && text[0] == '"':
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		text = strings.TrimSpace(text)
	}

	if number, err := strconv.ParseInt(text, 10, 64); err == nil {
		*i = integer(number)
		return nil
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(number) || number < math.MinInt64 || number > math.MaxInt64 {
		return fmt.Errorf("%.40s is not a number", data)
	}
	*i = integer(number)
	return nil
}

// Parse reads a body: one rpc, or a list of them
func Parse(body []byte) ([]pogo.Request, error) {
	var envelopes []envelope
	if trimmed := bytes.TrimLeft(body, " \t\r\n"); len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(body, &envelopes); err != nil {
			return nil, err
		}
	} else {
		envelopes = make([]envelope, 1)
		if err := json.Unmarshal(body, &envelopes[0]); err != nil {
			return nil, err
		}
	}

	requests := make([]pogo.Request, len(envelopes))
	for i := range envelopes {
		if err := envelopes[i].request(&requests[i]); err != nil {
			return nil, err
		}
	}
	return requests, nil
}

func (e *envelope) request(out *pogo.Request) error {
	entries := e.Contents
	if entries == nil {
		entries = e.Protos
	}
	if entries == nil {
		return errors.New("protos are missing")
	}

	if e.RPCID != nil {
		out.RPCID = int64(*e.RPCID)
	}
	if e.RPCStatus != nil {
		out.RPCStatus = int64(*e.RPCStatus)
	}
	if e.RPCHandle != nil {
		handle := int64(*e.RPCHandle)
		out.RPCHandle = &handle
	}

	// every proto's bytes go into one allocation of the size they need, so a record costs
	// what its traffic weighs and nothing gets rounded up per message
	size := 0
	for i := range *entries {
		entry := &(*entries)[i]
		if entry.Payload == nil {
			entry.Payload = entry.Response
		}
		size += decodedSize(entry.Request) + decodedSize(entry.Payload)
	}
	blob := make([]byte, 0, size)

	out.Protos = make([]pogo.RawProto, len(*entries))
	for i := range *entries {
		entry := &(*entries)[i]
		method := entry.Type
		if method == nil {
			method = entry.Method
		}
		if method == nil {
			return fmt.Errorf("proto %d has no method", i)
		}

		var err error
		proto := &out.Protos[i]
		proto.Method = int64(*method)
		if blob, proto.Request, err = appendDecoded(blob, entry.Request); err != nil {
			return fmt.Errorf("proto %d: the request is not base64", i)
		}
		if blob, proto.Response, err = appendDecoded(blob, entry.Payload); err != nil {
			return fmt.Errorf("proto %d: the response is not base64", i)
		}
	}
	return nil
}

func decodedSize(text *string) int {
	if text == nil {
		return 0
	}
	return base64.StdEncoding.DecodedLen(len(*text))
}

// appendDecoded decodes base64 onto the end of blob and returns the part it added. The blob
// has room for it, so it never moves and the parts handed out before stay valid
func appendDecoded(blob []byte, text *string) ([]byte, []byte, error) {
	if text == nil || *text == "" {
		return blob, nil, nil
	}

	// some mitms pad their strings with zero bytes
	trimmed := strings.TrimRight(*text, "\x00")
	start := len(blob)
	decoded, err := base64.StdEncoding.AppendDecode(blob, unsafe.Slice(unsafe.StringData(trimmed), len(trimmed)))
	if err != nil {
		// line breaks, or padding that is missing: decode what is base64 about it
		clean := make([]byte, 0, len(trimmed))
		for i := 0; i < len(trimmed); i++ {
			c := trimmed[i]
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' {
				clean = append(clean, c)
			}
		}
		decoded, err = base64.RawStdEncoding.AppendDecode(blob[:start], clean)
		if err != nil {
			return blob, nil, err
		}
	}
	return decoded, decoded[start:len(decoded):len(decoded)], nil
}

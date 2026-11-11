package pogo

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// RawProto is one method call the way a mitm sends it: still encoded
type RawProto struct {
	Method   int64
	Request  []byte
	Response []byte
}

// Request is everything a mitm sends about one rpc
type Request struct {
	RPCID     int64
	RPCStatus int64
	RPCHandle *int64
	Protos    []RawProto
}

// Message is the request or the response of a method
type Message struct {
	// Name of the message type, empty when the protos don't know the method
	Name string
	Raw  []byte
	// Payload is nil when the method is unknown or its bytes don't decode
	Payload protoreflect.Message
}

// Proto is a decoded method call
type Proto struct {
	// MethodName is empty when the protos don't know the method
	MethodName  string
	MethodValue int64
	Request     Message
	Response    Message
	// Proxy is the call a proxy request carries inside of it
	Proxy *Proto
}

var unmarshal = proto.UnmarshalOptions{
	// nothing ever looks at unknown fields, keeping them only costs memory
	DiscardUnknown: true,
	AllowPartial:   true,
}

// a proxied call could carry another one. Real traffic doesn't, garbage might try
const maxProxyDepth = 4

// Decode decodes a method call. It never fails: what doesn't decode is kept as raw bytes
func (r *Registry) Decode(raw RawProto) *Proto {
	return r.decode(raw, 0)
}

func (r *Registry) decode(raw RawProto, depth int) *Proto {
	name, _, value := r.resolveMethod(raw.Method)
	decoded := &Proto{MethodName: name, MethodValue: value}

	var requestType, responseType protoreflect.MessageDescriptor
	if value == int64(int32(value)) {
		requestType = r.requests[int32(value)]
		responseType = r.responses[int32(value)]
	}
	decoded.Request = decodeMessage(requestType, raw.Request)
	decoded.Response = decodeMessage(responseType, raw.Response)

	request, response := decoded.Request.Payload, decoded.Response.Payload
	if depth < maxProxyDepth && request != nil && response != nil && r.proxyRequest != nil &&
		request.Descriptor() == r.proxyRequest && response.Descriptor() == r.proxyResponse {
		decoded.Proxy = r.decode(RawProto{
			Method:   int64(request.Get(r.proxyRequest.Fields().ByName("action")).Uint()),
			Request:  request.Get(r.proxyRequest.Fields().ByName("payload")).Bytes(),
			Response: response.Get(r.proxyResponse.Fields().ByName("payload")).Bytes(),
		}, depth+1)
	}
	return decoded
}

func decodeMessage(descriptor protoreflect.MessageDescriptor, raw []byte) Message {
	message := Message{Raw: raw}
	if descriptor == nil {
		return message
	}

	message.Name = string(descriptor.Name())
	payload := dynamicpb.NewMessage(descriptor)
	if err := unmarshal.Unmarshal(raw, payload); err == nil {
		message.Payload = payload
	}
	return message
}

// State tells the web UI how a message decoded
func (m *Message) State() string {
	switch {
	case m.Name == "":
		return "unknown"
	case m.Payload == nil:
		return "error"
	case isEmpty(m.Payload):
		return "empty"
	}
	return "ok"
}

func isEmpty(message protoreflect.Message) bool {
	empty := true
	message.Range(func(protoreflect.FieldDescriptor, protoreflect.Value) bool {
		empty = false
		return false
	})
	return empty
}

// AppendText appends the message in protobuf's text format. What didn't decode is printed as
// the json of a best guess (see blackbox.go)
func (m *Message) AppendText(dst []byte, oneLine bool) []byte {
	if m.Payload == nil {
		return appendBlackboxText(dst, m.Raw, oneLine)
	}
	return AppendText(dst, m.Payload, oneLine)
}

// AppendJSON appends the message as json, the way protobuf's own json mapping does
func (m *Message) AppendJSON(dst []byte) []byte {
	if m.Payload == nil {
		return appendBlackboxJSON(dst, m.Raw)
	}
	return AppendJSON(dst, m.Payload)
}

// top level enums of responses that tell whether a call worked, i.e. EncounterOutProto.status
// or FortSearchOutProto.result
var statusFields = [2]protoreflect.Name{"status", "result"}

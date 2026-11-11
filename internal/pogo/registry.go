// Package pogo knows the game's protos: which message a method's request and response are,
// how to decode them, and how to print them.
//
// Nothing here is generated code. The descriptors of every message live in pogo.desc and are
// read with dynamicpb, so updating protos is replacing one file (see cmd/protoupdate).
package pogo

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

//go:embed pogo.desc
var embedded []byte

// every method's enum value is called REQUEST_TYPE_<name>
const methodPrefix = "REQUEST_TYPE_"

// methods the game sends through a proxy call don't have a message of their own number
const socialPrefix = "SOCIAL_ACTION_"

// Registry maps method numbers to the messages they use
type Registry struct {
	requests  map[int32]protoreflect.MessageDescriptor
	responses map[int32]protoreflect.MessageDescriptor
	methods   map[int32]string
	typeToID  map[string]int32

	proxyRequest  protoreflect.MessageDescriptor
	proxyResponse protoreflect.MessageDescriptor

	messages int
}

var defaultRegistry = sync.OnceValue(func() *Registry {
	registry, err := Load(embedded)
	if err != nil {
		panic("the built in protos are broken: " + err.Error())
	}
	return registry
})

// Default is the registry of the protos Traffic Light was built with
func Default() *Registry {
	return defaultRegistry()
}

// Load builds a registry from a serialized FileDescriptorProto
func Load(descriptor []byte) (*Registry, error) {
	fileProto := &descriptorpb.FileDescriptorProto{}
	if err := proto.Unmarshal(descriptor, fileProto); err != nil {
		return nil, err
	}
	file, err := protodesc.NewFile(fileProto, &protoregistry.Files{})
	if err != nil {
		return nil, err
	}

	all := file.Messages().ByName("AllTypesAndMessagesResponsesProto")
	if all == nil {
		return nil, errors.New("AllTypesAndMessagesResponsesProto is missing")
	}
	allRequests := all.Messages().ByName("AllMessagesProto")
	allResponses := all.Messages().ByName("AllResponsesProto")
	// the typo is the protos' own
	allMethods := all.Enums().ByName("AllResquestTypesProto")
	if allRequests == nil || allResponses == nil || allMethods == nil {
		return nil, errors.New("AllTypesAndMessagesResponsesProto doesn't list the methods and their messages")
	}

	registry := &Registry{
		requests:      messagesByNumber(allRequests),
		responses:     messagesByNumber(allResponses),
		methods:       make(map[int32]string, allMethods.Values().Len()),
		typeToID:      make(map[string]int32, allRequests.Fields().Len()),
		proxyRequest:  file.Messages().ByName("ProxyRequestProto"),
		proxyResponse: file.Messages().ByName("ProxyResponseProto"),
		messages:      file.Messages().Len(),
	}

	values := allMethods.Values()
	for i := 0; i < values.Len(); i++ {
		value := values.Get(i)
		registry.methods[int32(value.Number())] = strings.TrimPrefix(string(value.Name()), methodPrefix)
	}
	for number, message := range registry.requests {
		registry.typeToID[string(message.Name())] = number
	}

	if registry.proxyRequest != nil && registry.proxyResponse != nil {
		for _, field := range []protoreflect.FieldDescriptor{
			registry.proxyRequest.Fields().ByName("action"),
			registry.proxyRequest.Fields().ByName("payload"),
			registry.proxyResponse.Fields().ByName("payload"),
		} {
			if field == nil {
				return nil, fmt.Errorf("the proxy messages changed their fields")
			}
		}
	}
	return registry, nil
}

func messagesByNumber(listing protoreflect.MessageDescriptor) map[int32]protoreflect.MessageDescriptor {
	fields := listing.Fields()
	out := make(map[int32]protoreflect.MessageDescriptor, fields.Len())
	for i := 0; i < fields.Len(); i++ {
		if message := fields.Get(i).Message(); message != nil {
			out[int32(fields.Get(i).Number())] = message
		}
	}
	return out
}

// Messages is how many top level messages the protos define
func (r *Registry) Messages() int { return r.messages }

// Methods is how many methods the protos know by name
func (r *Registry) Methods() int { return len(r.methods) }

func (r *Registry) methodName(value int64) (string, bool) {
	if value != int64(int32(value)) {
		return "", false
	}
	name, ok := r.methods[int32(value)]
	return name, ok
}

// resolveMethod names a method. A social action without a message of its own number is mapped
// to the method whose request is named after it, i.e. SOCIAL_ACTION_GET_INBOX to the one that
// takes a GetInboxProto. That is how proxied calls find their messages.
func (r *Registry) resolveMethod(value int64) (name string, known bool, resolved int64) {
	name, known = r.methodName(value)
	if !known || !strings.HasPrefix(name, socialPrefix) || r.hasMessage(value) {
		return name, known, value
	}

	base := camelCase(name[len(socialPrefix):])
	for _, candidate := range [2]string{base + "V2Proto", base + "Proto"} {
		number, ok := r.typeToID[candidate]
		if !ok {
			continue
		}
		name, known = r.methodName(int64(number))
		if known {
			return name, true, int64(number)
		}
	}
	return name, known, value
}

func (r *Registry) hasMessage(value int64) bool {
	if value != int64(int32(value)) {
		return false
	}
	_, request := r.requests[int32(value)]
	_, response := r.responses[int32(value)]
	return request || response
}

// camelCase turns GET_INBOX into GetInbox
func camelCase(name string) string {
	out := make([]byte, 0, len(name))
	start := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_':
			start = true
			continue
		case c >= 'A' && c <= 'Z':
			if !start {
				c += 'a' - 'A'
			}
			start = false
		case c >= 'a' && c <= 'z':
			if start {
				c -= 'a' - 'A'
			}
			start = false
		default:
			// a digit starts a new word, like Python's str.title
			start = true
		}
		out = append(out, c)
	}
	return string(out)
}

// RequestType is the message a method's request is, or nil if the protos don't know the method
func (r *Registry) RequestType(method int32) protoreflect.MessageDescriptor {
	return r.requests[method]
}

// ResponseType is the message a method's response is, or nil if the protos don't know the method
func (r *Registry) ResponseType(method int32) protoreflect.MessageDescriptor {
	return r.responses[method]
}

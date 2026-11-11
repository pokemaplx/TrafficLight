package decode

import (
	"math"
	"strconv"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// AppendTree appends a message as the tree the web UI's inspector draws. Unlike the json, it
// keeps the type information. Every value is a node:
//
//	{"k": "msg", "t": type name, "f": [[field name, type name, is repeated, node or list of nodes], ...]}
//	{"k": "enum", "v": number, "e": name or null}, {"k": "bytes", "v": escaped, "n": length}
//	{"k": "str" | "num" | "bool", "v": value}
func AppendTree(dst []byte, message protoreflect.Message) []byte {
	writer := treeWriter{out: dst}
	writer.message(message)
	return writer.out
}

type treeWriter struct {
	out    []byte
	fields fieldStack
}

func (w *treeWriter) message(message protoreflect.Message) {
	w.open(message.Descriptor())
	start, end := w.fields.push(message)
	// message fields come last, like in the terminal UI Traffic Light used to have
	first := true
	for _, messages := range [2]bool{false, true} {
		for i := start; i < end; i++ {
			field, value := w.fields[i].field, w.fields[i].value
			if isMessage(field) != messages {
				continue
			}
			if !first {
				w.out = append(w.out, ',')
			}
			first = false
			w.entry(field, value)
		}
	}
	w.fields.pop(start)
	w.out = append(w.out, ']', '}')
}

func (w *treeWriter) open(descriptor protoreflect.MessageDescriptor) {
	w.out = append(w.out, `{"k":"msg","t":`...)
	w.out = AppendJSONString(w.out, string(descriptor.Name()))
	w.out = append(w.out, `,"f":[`...)
}

func (w *treeWriter) entry(field protoreflect.FieldDescriptor, value protoreflect.Value) {
	w.out = append(w.out, '[')
	w.out = AppendJSONString(w.out, string(field.Name()))
	w.out = append(w.out, ',')
	w.out = AppendJSONString(w.out, typeName(field))

	switch {
	case field.IsMap():
		w.out = append(w.out, `,true,[`...)
		entries := value.Map()
		for i, key := range sortedKeys(entries) {
			if i > 0 {
				w.out = append(w.out, ',')
			}
			w.mapEntry(field, key, entries.Get(key))
		}
		w.out = append(w.out, ']')
	case field.IsList():
		w.out = append(w.out, `,true,[`...)
		list := value.List()
		for i := 0; i < list.Len(); i++ {
			if i > 0 {
				w.out = append(w.out, ',')
			}
			w.value(field, list.Get(i))
		}
		w.out = append(w.out, ']')
	default:
		w.out = append(w.out, `,false,`...)
		w.value(field, value)
	}
	w.out = append(w.out, ']')
}

// mapEntry draws a map's entry as the message it is on the wire, where zero values aren't set
func (w *treeWriter) mapEntry(field protoreflect.FieldDescriptor, key protoreflect.MapKey, value protoreflect.Value) {
	keyField, valueField := field.MapKey(), field.MapValue()
	w.open(field.Message())
	first := true
	if !isDefault(keyField, key.Value()) {
		w.entry(keyField, key.Value())
		first = false
	}
	if isMessage(valueField) || !isDefault(valueField, value) {
		if !first {
			w.out = append(w.out, ',')
		}
		w.entry(valueField, value)
	}
	w.out = append(w.out, ']', '}')
}

func (w *treeWriter) value(field protoreflect.FieldDescriptor, value protoreflect.Value) {
	switch kind := field.Kind(); kind {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		w.message(value.Message())
	case protoreflect.EnumKind:
		number := value.Enum()
		w.out = append(w.out, `{"k":"enum","v":`...)
		w.out = strconv.AppendInt(w.out, int64(number), 10)
		w.out = append(w.out, `,"e":`...)
		if known := field.Enum().Values().ByNumber(number); known != nil {
			w.out = AppendJSONString(w.out, string(known.Name()))
		} else {
			w.out = append(w.out, "null"...)
		}
		w.out = append(w.out, '}')
	case protoreflect.StringKind:
		w.out = append(w.out, `{"k":"str","v":`...)
		w.out = AppendJSONString(w.out, value.String())
		w.out = append(w.out, '}')
	case protoreflect.BytesKind:
		data := value.Bytes()
		w.out = append(w.out, `{"k":"bytes","v":`...)
		w.out = AppendJSONBytes(w.out, AppendEscapedBytes(nil, data))
		w.out = append(w.out, `,"n":`...)
		w.out = strconv.AppendInt(w.out, int64(len(data)), 10)
		w.out = append(w.out, '}')
	case protoreflect.BoolKind:
		if value.Bool() {
			w.out = append(w.out, `{"k":"bool","v":true}`...)
		} else {
			w.out = append(w.out, `{"k":"bool","v":false}`...)
		}
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		number := value.Float()
		w.out = append(w.out, `{"k":"num","v":`...)
		if math.IsNaN(number) || math.IsInf(number, 0) {
			// nan and inf aren't valid json
			w.out = append(w.out, '"')
			w.out = appendFloat(w.out, number, 64)
			w.out = append(w.out, '"')
		} else {
			w.out = appendFloat(w.out, number, floatBits(field))
		}
		w.out = append(w.out, '}')
	default:
		w.out = append(w.out, `{"k":"num","v":`...)
		if is64Bit(kind) {
			// javascript numbers can't hold every 64 bit integer, i.e. encounter ids
			w.out = append(w.out, '"')
			w.out = appendInteger(w.out, field, value)
			w.out = append(w.out, '"')
		} else {
			w.out = appendInteger(w.out, field, value)
		}
		w.out = append(w.out, '}')
	}
}

var scalarNames = map[protoreflect.Kind]string{
	protoreflect.DoubleKind:   "double",
	protoreflect.FloatKind:    "float",
	protoreflect.Int64Kind:    "int64",
	protoreflect.Uint64Kind:   "uint64",
	protoreflect.Int32Kind:    "int32",
	protoreflect.Fixed64Kind:  "fixed64",
	protoreflect.Fixed32Kind:  "fixed32",
	protoreflect.BoolKind:     "bool",
	protoreflect.StringKind:   "string",
	protoreflect.BytesKind:    "bytes",
	protoreflect.Uint32Kind:   "uint32",
	protoreflect.Sfixed32Kind: "sfixed32",
	protoreflect.Sfixed64Kind: "sfixed64",
	protoreflect.Sint32Kind:   "sint32",
	protoreflect.Sint64Kind:   "sint64",
}

func typeName(field protoreflect.FieldDescriptor) string {
	if message := field.Message(); message != nil {
		return string(message.Name())
	}
	if enum := field.Enum(); enum != nil {
		return string(enum.Name())
	}
	if name, ok := scalarNames[field.Kind()]; ok {
		return name
	}
	return "unknown"
}

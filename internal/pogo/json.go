package pogo

import (
	"encoding/base64"
	"math"
	"strconv"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// AppendJSON appends a message as json with the fields' own names: 64 bit integers as strings
// (javascript can't hold them), bytes as base64, enums by name. Only what is set is listed.
func AppendJSON(dst []byte, message protoreflect.Message) []byte {
	writer := jsonWriter{out: dst}
	writer.message(message)
	return writer.out
}

type jsonWriter struct {
	out    []byte
	fields fieldStack
}

func (w *jsonWriter) message(message protoreflect.Message) {
	w.out = append(w.out, '{')
	start, end := w.fields.push(message)
	for i := start; i < end; i++ {
		field, value := w.fields[i].field, w.fields[i].value
		if i > start {
			w.out = append(w.out, ',')
		}
		w.out = AppendJSONString(w.out, string(field.Name()))
		w.out = append(w.out, ':')

		switch {
		case field.IsMap():
			w.entries(field, value.Map())
		case field.IsList():
			list := value.List()
			w.out = append(w.out, '[')
			for j := 0; j < list.Len(); j++ {
				if j > 0 {
					w.out = append(w.out, ',')
				}
				w.value(field, list.Get(j))
			}
			w.out = append(w.out, ']')
		default:
			w.value(field, value)
		}
	}
	w.fields.pop(start)
	w.out = append(w.out, '}')
}

func (w *jsonWriter) entries(field protoreflect.FieldDescriptor, entries protoreflect.Map) {
	key, value := field.MapKey(), field.MapValue()
	w.out = append(w.out, '{')
	for i, entryKey := range sortedKeys(entries) {
		if i > 0 {
			w.out = append(w.out, ',')
		}
		// json only has string keys
		switch key.Kind() {
		case protoreflect.StringKind:
			w.out = AppendJSONString(w.out, entryKey.String())
		case protoreflect.BoolKind:
			if entryKey.Bool() {
				w.out = append(w.out, `"true"`...)
			} else {
				w.out = append(w.out, `"false"`...)
			}
		default:
			w.out = append(w.out, '"')
			w.out = appendInteger(w.out, key, entryKey.Value())
			w.out = append(w.out, '"')
		}
		w.out = append(w.out, ':')
		w.value(value, entries.Get(entryKey))
	}
	w.out = append(w.out, '}')
}

func (w *jsonWriter) value(field protoreflect.FieldDescriptor, value protoreflect.Value) {
	switch kind := field.Kind(); kind {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		w.message(value.Message())
	case protoreflect.BoolKind:
		if value.Bool() {
			w.out = append(w.out, "true"...)
		} else {
			w.out = append(w.out, "false"...)
		}
	case protoreflect.EnumKind:
		number := value.Enum()
		if known := field.Enum().Values().ByNumber(number); known != nil {
			w.out = AppendJSONString(w.out, string(known.Name()))
		} else {
			w.out = strconv.AppendInt(w.out, int64(number), 10)
		}
	case protoreflect.StringKind:
		w.out = AppendJSONString(w.out, value.String())
	case protoreflect.BytesKind:
		w.out = append(w.out, '"')
		w.out = base64.StdEncoding.AppendEncode(w.out, value.Bytes())
		w.out = append(w.out, '"')
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		number := value.Float()
		switch {
		case math.IsNaN(number):
			w.out = append(w.out, `"NaN"`...)
		case math.IsInf(number, 1):
			w.out = append(w.out, `"Infinity"`...)
		case math.IsInf(number, -1):
			w.out = append(w.out, `"-Infinity"`...)
		default:
			w.out = appendFloat(w.out, number, floatBits(field))
		}
	default:
		if is64Bit(kind) {
			w.out = append(w.out, '"')
			w.out = appendInteger(w.out, field, value)
			w.out = append(w.out, '"')
		} else {
			w.out = appendInteger(w.out, field, value)
		}
	}
}

package pogo

import (
	"bytes"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// AppendText appends a message in protobuf's text format, the same way Python's text_format
// prints it (prototext's output is unstable on purpose, and the web UI highlights and searches
// this text). One line: `a: 1 b { c: "x" }`. Otherwise one field per line, indented by two.
func AppendText(dst []byte, message protoreflect.Message, oneLine bool) []byte {
	writer := textWriter{out: dst, oneLine: oneLine}
	writer.message(message, 0)
	if oneLine {
		// every field is followed by a space
		writer.out = bytes.TrimRight(writer.out, " ")
	}
	return writer.out
}

type textWriter struct {
	out     []byte
	oneLine bool
	fields  fieldStack
}

func (w *textWriter) message(message protoreflect.Message, indent int) {
	start, end := w.fields.push(message)
	for i := start; i < end; i++ {
		field, value := w.fields[i].field, w.fields[i].value
		switch {
		case field.IsMap():
			w.entries(field, value.Map(), indent)
		case field.IsList():
			list := value.List()
			for j := 0; j < list.Len(); j++ {
				w.field(field, list.Get(j), indent)
			}
		default:
			w.field(field, value, indent)
		}
	}
	w.fields.pop(start)
}

func (w *textWriter) field(field protoreflect.FieldDescriptor, value protoreflect.Value, indent int) {
	w.name(field, indent)
	if isMessage(field) {
		w.open()
		w.message(value.Message(), indent+2)
		w.close(indent)
	} else {
		w.out = append(w.out, ':', ' ')
		w.scalar(field, value)
	}
	w.end()
}

// entries prints a map the way it is on the wire: a repeated message of key and value
func (w *textWriter) entries(field protoreflect.FieldDescriptor, entries protoreflect.Map, indent int) {
	key, value := field.MapKey(), field.MapValue()
	for _, entryKey := range sortedKeys(entries) {
		w.name(field, indent)
		w.open()
		if !isDefault(key, entryKey.Value()) {
			w.field(key, entryKey.Value(), indent+2)
		}
		if entryValue := entries.Get(entryKey); isMessage(value) || !isDefault(value, entryValue) {
			w.field(value, entryValue, indent+2)
		}
		w.close(indent)
		w.end()
	}
}

func (w *textWriter) name(field protoreflect.FieldDescriptor, indent int) {
	if !w.oneLine {
		w.indent(indent)
	}
	w.out = append(w.out, field.Name()...)
}

func (w *textWriter) open() {
	if w.oneLine {
		w.out = append(w.out, ' ', '{', ' ')
	} else {
		w.out = append(w.out, ' ', '{', '\n')
	}
}

func (w *textWriter) close(indent int) {
	if !w.oneLine {
		w.indent(indent)
	}
	w.out = append(w.out, '}')
}

func (w *textWriter) end() {
	if w.oneLine {
		w.out = append(w.out, ' ')
	} else {
		w.out = append(w.out, '\n')
	}
}

func (w *textWriter) indent(indent int) {
	for ; indent > 0; indent-- {
		w.out = append(w.out, ' ')
	}
}

func (w *textWriter) scalar(field protoreflect.FieldDescriptor, value protoreflect.Value) {
	switch field.Kind() {
	case protoreflect.BoolKind:
		if value.Bool() {
			w.out = append(w.out, "true"...)
		} else {
			w.out = append(w.out, "false"...)
		}
	case protoreflect.EnumKind:
		w.out = appendEnum(w.out, field, value.Enum())
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		w.out = appendFloat(w.out, value.Float(), floatBits(field))
	case protoreflect.StringKind:
		w.out = append(w.out, '"')
		w.out = appendEscapedText(w.out, value.String())
		w.out = append(w.out, '"')
	case protoreflect.BytesKind:
		w.out = append(w.out, '"')
		w.out = AppendEscapedBytes(w.out, value.Bytes())
		w.out = append(w.out, '"')
	default:
		w.out = appendInteger(w.out, field, value)
	}
}

// appendEscapedText escapes a string for the text format, leaving everything readable as it
// is: only quotes, backslashes and line breaks are escaped
func appendEscapedText(dst []byte, text string) []byte {
	start := 0
	for i := 0; i < len(text); i++ {
		var escaped byte
		switch text[i] {
		case '\t':
			escaped = 't'
		case '\n':
			escaped = 'n'
		case '\r':
			escaped = 'r'
		case '"', '\'', '\\':
			escaped = text[i]
		default:
			continue
		}
		dst = append(dst, text[start:i]...)
		dst = append(dst, '\\', escaped)
		start = i + 1
	}
	return append(dst, text[start:]...)
}

// AppendEscapedBytes escapes bytes the way C does: printable ascii stays, the rest turns into
// octal escapes like \001
func AppendEscapedBytes(dst []byte, data []byte) []byte {
	for _, c := range data {
		switch c {
		case '\t':
			dst = append(dst, '\\', 't')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '"', '\'', '\\':
			dst = append(dst, '\\', c)
		default:
			if c >= 32 && c < 127 {
				dst = append(dst, c)
			} else {
				dst = append(dst, '\\', '0'+c>>6, '0'+(c>>3)&7, '0'+c&7)
			}
		}
	}
	return dst
}

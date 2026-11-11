package pogo

import (
	"bytes"
	"cmp"
	"math"
	"slices"
	"strconv"
	"unicode/utf8"
	"unsafe"

	"google.golang.org/protobuf/reflect/protoreflect"
)

type fieldValue struct {
	field protoreflect.FieldDescriptor
	value protoreflect.Value
}

// fieldStack lists the set fields of nested messages by field number without allocating for
// every message: a message's fields sit on top of its parent's and are popped when it is done
type fieldStack []fieldValue

func (s *fieldStack) push(message protoreflect.Message) (start, end int) {
	start = len(*s)
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		*s = append(*s, fieldValue{field, value})
		return true
	})
	slices.SortFunc((*s)[start:], func(a, b fieldValue) int {
		return cmp.Compare(a.field.Number(), b.field.Number())
	})
	return start, len(*s)
}

func (s *fieldStack) pop(start int) {
	clear((*s)[start:])
	*s = (*s)[:start]
}

func isMessage(field protoreflect.FieldDescriptor) bool {
	return field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind
}

// sortedKeys returns a map's keys in the order protobuf's text format prints them
func sortedKeys(entries protoreflect.Map) []protoreflect.MapKey {
	keys := make([]protoreflect.MapKey, 0, entries.Len())
	entries.Range(func(key protoreflect.MapKey, _ protoreflect.Value) bool {
		keys = append(keys, key)
		return true
	})
	slices.SortFunc(keys, func(a, b protoreflect.MapKey) int {
		switch x := a.Interface().(type) {
		case bool:
			if x == b.Bool() {
				return 0
			}
			if !x {
				return -1
			}
			return 1
		case int32, int64:
			return cmp.Compare(a.Int(), b.Int())
		case uint32, uint64:
			return cmp.Compare(a.Uint(), b.Uint())
		}
		return cmp.Compare(a.String(), b.String())
	})
	return keys
}

// isDefault tells whether a scalar is its type's zero value. A map entry is a message of its
// own, and like in any proto3 message, zero values aren't set in it
func isDefault(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
	switch field.Kind() {
	case protoreflect.BoolKind:
		return !value.Bool()
	case protoreflect.EnumKind:
		return value.Enum() == 0
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return value.Int() == 0
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return value.Uint() == 0
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return value.Float() == 0
	case protoreflect.StringKind:
		return value.String() == ""
	case protoreflect.BytesKind:
		return len(value.Bytes()) == 0
	}
	return false
}

func is64Bit(kind protoreflect.Kind) bool {
	switch kind {
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return true
	}
	return false
}

func isUnsigned(kind protoreflect.Kind) bool {
	switch kind {
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return true
	}
	return false
}

func appendInteger(dst []byte, field protoreflect.FieldDescriptor, value protoreflect.Value) []byte {
	if isUnsigned(field.Kind()) {
		return strconv.AppendUint(dst, value.Uint(), 10)
	}
	return strconv.AppendInt(dst, value.Int(), 10)
}

// appendEnum appends an enum value's name, or its number when the protos don't know it
func appendEnum(dst []byte, field protoreflect.FieldDescriptor, number protoreflect.EnumNumber) []byte {
	if value := field.Enum().Values().ByNumber(number); value != nil {
		return append(dst, value.Name()...)
	}
	return strconv.AppendInt(dst, int64(number), 10)
}

func floatBits(field protoreflect.FieldDescriptor) int {
	if field.Kind() == protoreflect.FloatKind {
		return 32
	}
	return 64
}

// appendFloat appends the shortest text that still reads back as the same number, 0.9 instead
// of 0.8999999761581421, in the notation Python's repr uses: 100.0, 1e-05, 1.5e+20.
// Finite numbers are valid json that way.
func appendFloat(dst []byte, value float64, bits int) []byte {
	switch {
	case math.IsNaN(value):
		return append(dst, "nan"...)
	case math.IsInf(value, 1):
		return append(dst, "inf"...)
	case math.IsInf(value, -1):
		return append(dst, "-inf"...)
	}

	var scratch [32]byte
	scientific := strconv.AppendFloat(scratch[:0], value, 'e', -1, bits)
	exponent, _ := strconv.Atoi(string(scientific[bytes.LastIndexByte(scientific, 'e')+1:]))
	if exponent < -4 || exponent >= 16 {
		return append(dst, scientific...)
	}

	start := len(dst)
	dst = strconv.AppendFloat(dst, value, 'f', -1, bits)
	if bytes.IndexByte(dst[start:], '.') < 0 {
		dst = append(dst, ".0"...)
	}
	return dst
}

const hexDigits = "0123456789abcdef"

// AppendJSONString appends s as a json string, quotes included
func AppendJSONString(dst []byte, s string) []byte {
	return AppendJSONBytes(dst, unsafe.Slice(unsafe.StringData(s), len(s)))
}

// AppendJSONBytes appends text as a json string, quotes included. Text that isn't valid UTF-8
// gets the replacement character, so the result always is valid json
func AppendJSONBytes(dst []byte, text []byte) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(text); {
		c := text[i]
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRune(text[i:])
			if r == utf8.RuneError && size == 1 {
				dst = append(dst, text[start:i]...)
				dst = append(dst, `�`...)
				i++
				start = i
				continue
			}
			i += size
			continue
		}
		if c >= ' ' && c != '"' && c != '\\' {
			i++
			continue
		}

		dst = append(dst, text[start:i]...)
		switch c {
		case '"', '\\':
			dst = append(dst, '\\', c)
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
		}
		i++
		start = i
	}
	dst = append(dst, text[start:]...)
	return append(dst, '"')
}

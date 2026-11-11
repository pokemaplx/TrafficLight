package pogo

import (
	"encoding/binary"
	"strconv"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

// Messages of methods the protos don't know are still protobuf, just without field names or
// types. This guesses its way through them the way blackboxprotobuf does: fields are keyed by
// their number, varints are signed integers, and a length delimited field is a message if all
// of its values parse as one, else a string if they are valid UTF-8, else bytes (shown with
// one character per byte). A field that shows up more than once is a list.
//
// blackboxprotobuf also remembers what a field was in the message before, and names fields
// like "5-1" when it changes its mind. This doesn't: every message is guessed on its own.

type blackboxKind uint8

const (
	blackboxInt blackboxKind = iota
	blackboxUint
	blackboxString
	blackboxMessage
	blackboxList
)

type blackboxNode struct {
	kind     blackboxKind
	signed   int64
	unsigned uint64
	text     string
	fields   []blackboxField
	list     []blackboxNode
}

type blackboxField struct {
	number uint64
	value  blackboxNode
}

// bytes that happen to look like nested messages could otherwise nest without end
const maxBlackboxDepth = 64

// decodeBlackbox returns false for bytes that aren't protobuf at all
func decodeBlackbox(data []byte, depth int) ([]blackboxField, bool) {
	type group struct {
		number uint64
		wire   protowire.Type
		parts  [][]byte
	}
	var groups []group
	index := map[uint64]int{}

	for len(data) > 0 {
		tag, size := protowire.ConsumeVarint(data)
		if size < 0 {
			return nil, false
		}
		data = data[size:]
		number, wire := tag>>3, protowire.Type(tag&7)

		var part []byte
		switch wire {
		case protowire.VarintType:
			_, size = protowire.ConsumeVarint(data)
			if size < 0 {
				return nil, false
			}
			part, data = data[:size], data[size:]
		case protowire.Fixed32Type:
			if len(data) < 4 {
				return nil, false
			}
			part, data = data[:4], data[4:]
		case protowire.Fixed64Type:
			if len(data) < 8 {
				return nil, false
			}
			part, data = data[:8], data[8:]
		case protowire.BytesType:
			part, size = protowire.ConsumeBytes(data)
			if size < 0 {
				return nil, false
			}
			data = data[size:]
		default:
			// groups, or a wire type that doesn't exist
			return nil, false
		}

		position, seen := index[number]
		if !seen {
			position = len(groups)
			index[number] = position
			groups = append(groups, group{number: number, wire: wire})
		} else if groups[position].wire != wire {
			return nil, false
		}
		groups[position].parts = append(groups[position].parts, part)
	}

	fields := make([]blackboxField, len(groups))
	for i, group := range groups {
		var values []blackboxNode
		if group.wire == protowire.BytesType {
			values = guessValues(group.parts, depth)
		} else {
			values = make([]blackboxNode, len(group.parts))
			for j, part := range group.parts {
				switch group.wire {
				case protowire.VarintType:
					value, _ := protowire.ConsumeVarint(part)
					values[j] = blackboxNode{kind: blackboxInt, signed: int64(value)}
				case protowire.Fixed32Type:
					values[j] = blackboxNode{kind: blackboxUint, unsigned: uint64(binary.LittleEndian.Uint32(part))}
				default:
					values[j] = blackboxNode{kind: blackboxUint, unsigned: binary.LittleEndian.Uint64(part)}
				}
			}
		}

		fields[i].number = group.number
		if len(values) == 1 {
			fields[i].value = values[0]
		} else {
			fields[i].value = blackboxNode{kind: blackboxList, list: values}
		}
	}
	return fields, true
}

// guessValues decides what the values of a length delimited field are. All of them get the
// same type, since they are the same field
func guessValues(parts [][]byte, depth int) []blackboxNode {
	values := make([]blackboxNode, len(parts))

	messages := depth < maxBlackboxDepth
	for i := 0; messages && i < len(parts); i++ {
		fields, ok := decodeBlackbox(parts[i], depth+1)
		messages = ok
		values[i] = blackboxNode{kind: blackboxMessage, fields: fields}
	}
	if messages {
		return values
	}

	text := true
	for _, part := range parts {
		text = text && utf8.Valid(part)
	}
	for i, part := range parts {
		if text {
			values[i] = blackboxNode{kind: blackboxString, text: string(part)}
			continue
		}
		// one character per byte, so ascii inside of binary data stays readable
		runes := make([]rune, len(part))
		for j, c := range part {
			runes[j] = rune(c)
		}
		values[i] = blackboxNode{kind: blackboxString, text: string(runes)}
	}
	return values
}

type blackboxStyle uint8

const (
	// no whitespace at all
	blackboxCompact blackboxStyle = iota
	// `{"1": 2, "3": "x"}`, what the web UI searches in
	blackboxOneLine
	// one value per line, indented by two
	blackboxIndented
)

func appendBlackboxJSON(dst []byte, raw []byte) []byte {
	return appendBlackbox(dst, raw, blackboxCompact)
}

func appendBlackboxText(dst []byte, raw []byte, oneLine bool) []byte {
	if oneLine {
		return appendBlackbox(dst, raw, blackboxOneLine)
	}
	return appendBlackbox(dst, raw, blackboxIndented)
}

func appendBlackbox(dst []byte, raw []byte, style blackboxStyle) []byte {
	fields, ok := decodeBlackbox(raw, 0)
	if !ok {
		return append(dst, "null"...)
	}
	writer := blackboxWriter{out: dst, style: style}
	writer.node(blackboxNode{kind: blackboxMessage, fields: fields}, 0)
	return writer.out
}

type blackboxWriter struct {
	out   []byte
	style blackboxStyle
}

func (w *blackboxWriter) node(node blackboxNode, indent int) {
	switch node.kind {
	case blackboxInt:
		w.out = strconv.AppendInt(w.out, node.signed, 10)
	case blackboxUint:
		w.out = strconv.AppendUint(w.out, node.unsigned, 10)
	case blackboxString:
		w.out = AppendJSONString(w.out, node.text)
	case blackboxList:
		w.out = append(w.out, '[')
		for i, item := range node.list {
			w.separate(i, indent+2)
			w.node(item, indent+2)
		}
		w.end(len(node.list), indent, ']')
	case blackboxMessage:
		w.out = append(w.out, '{')
		for i, field := range node.fields {
			w.separate(i, indent+2)
			w.out = append(w.out, '"')
			w.out = strconv.AppendUint(w.out, field.number, 10)
			w.out = append(w.out, '"', ':')
			if w.style != blackboxCompact {
				w.out = append(w.out, ' ')
			}
			w.node(field.value, indent+2)
		}
		w.end(len(node.fields), indent, '}')
	}
}

func (w *blackboxWriter) separate(position int, indent int) {
	if position > 0 {
		w.out = append(w.out, ',')
		if w.style == blackboxOneLine {
			w.out = append(w.out, ' ')
		}
	}
	w.newline(indent)
}

func (w *blackboxWriter) end(count int, indent int, bracket byte) {
	if count > 0 {
		w.newline(indent)
	}
	w.out = append(w.out, bracket)
}

func (w *blackboxWriter) newline(indent int) {
	if w.style != blackboxIndented {
		return
	}
	w.out = append(w.out, '\n')
	for ; indent > 0; indent-- {
		w.out = append(w.out, ' ')
	}
}

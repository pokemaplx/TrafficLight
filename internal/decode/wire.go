package decode

import (
	"bytes"
	"cmp"
	"errors"
	"math"
	"slices"
	"strconv"
	"sync"
	"unicode/utf8"
	"unsafe"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Listing a record for a browser needs the one line text of its messages, and that is all it
// needs. Decoding a message into objects first costs about forty times its size in memory
// that is thrown away a moment later, for every record of the log, whenever a browser connects.
//
// The walker writes that text straight from the bytes: it looks the fields up in the
// descriptors and prints them as it meets them, without ever building the message. It has to
// say exactly what decoding and then printing says (see AppendText), which is what
// TestTheWalkerAgreesWithTheDecoder holds it to. That takes some care, since protobuf allows
// more than encoders ever do: fields in any order, repeated ones scattered, a message split
// into pieces, a oneof set more than once.

var (
	// the bytes aren't a valid message of this type
	errMalformed = errors.New("malformed message")
	// valid, but something the walker leaves to the decoder
	errUnsupported = errors.New("unsupported by the walker")
)

// messages nest this deep at most, like in protobuf's own decoder
const maxWireDepth = 10000

// wireEntry is one field as it is on the wire
type wireEntry struct {
	// the payload of a length delimited field
	data []byte
	// the value of a varint or fixed field
	bits uint64
	num  protowire.Number
	// its position in the message
	seq int32
	typ protowire.Type
}

type oneofState struct {
	// the member that was set last, which is the one that counts
	winner    protowire.Number
	winnerSeq int32
	// where another member was last set. What the winner had before that is gone
	otherSeq int32
}

type mapEntry struct {
	keyData   []byte
	valueData []byte
	keyBits   uint64
	valueBits uint64
	seq       int32
	hasValue  bool
}

type walker struct {
	out []byte
	// scratch space, used as stacks: a message's entries sit on top of its parent's
	entries []wireEntry
	oneofs  []oneofState
	maps    []mapEntry
}

var walkerPool = sync.Pool{New: func() any { return new(walker) }}

// a walker that grew for something huge isn't kept
const maxPooledWalker = 4 << 20

func getWalker() *walker {
	return walkerPool.Get().(*walker)
}

func putWalker(w *walker) {
	if cap(w.out) > maxPooledWalker || cap(w.entries) > maxPooledWalker/48 {
		return
	}
	w.out = w.out[:0]
	walkerPool.Put(w)
}

// text appends the one line text of a message to w.out and tells whether it has no fields.
// On an error, w.out is as it was before
func (w *walker) text(descriptor protoreflect.MessageDescriptor, raw []byte) (empty bool, err error) {
	start := len(w.out)
	w.entries, w.oneofs, w.maps = w.entries[:0], w.oneofs[:0], w.maps[:0]

	first := len(w.entries)
	if _, err = w.scan(raw, 0); err == nil {
		err = w.message(descriptor, first, 0)
	}
	if err != nil {
		w.out = w.out[:start]
		return false, err
	}
	// every field is followed by a space
	for len(w.out) > start && w.out[len(w.out)-1] == ' ' {
		w.out = w.out[:len(w.out)-1]
	}
	return len(w.out) == start, nil
}

// scan reads a message's fields onto the entry stack
func (w *walker) scan(data []byte, seq int32) (int32, error) {
	for len(data) > 0 {
		num, typ, size := protowire.ConsumeTag(data)
		if size < 0 || num > protowire.MaxValidNumber {
			return seq, errMalformed
		}
		data = data[size:]

		entry := wireEntry{num: num, typ: typ, seq: seq}
		switch typ {
		case protowire.VarintType:
			entry.bits, size = protowire.ConsumeVarint(data)
		case protowire.Fixed32Type:
			var value uint32
			value, size = protowire.ConsumeFixed32(data)
			entry.bits = uint64(value)
		case protowire.Fixed64Type:
			entry.bits, size = protowire.ConsumeFixed64(data)
		case protowire.BytesType:
			entry.data, size = protowire.ConsumeBytes(data)
		case protowire.StartGroupType:
			// proto3 has no groups, so this can only be a field the protos don't know
			_, size = protowire.ConsumeGroup(num, data)
			if size < 0 {
				return seq, errMalformed
			}
			data = data[size:]
			continue
		default:
			return seq, errMalformed
		}
		if size < 0 {
			return seq, errMalformed
		}
		data = data[size:]
		w.entries = append(w.entries, entry)
		seq++
	}
	return seq, nil
}

// message prints the fields that were scanned onto the stack from first on, and pops them
func (w *walker) message(descriptor protoreflect.MessageDescriptor, first int, depth int) error {
	if depth > maxWireDepth {
		return errMalformed
	}
	end := len(w.entries)
	defer func() { w.entries = w.entries[:first] }()

	// encoders write fields in the order of their numbers, so this rarely has anything to do
	ordered := func(a, b wireEntry) int {
		if a.num != b.num {
			return cmp.Compare(a.num, b.num)
		}
		return cmp.Compare(a.seq, b.seq)
	}
	if !slices.IsSortedFunc(w.entries[first:end], ordered) {
		slices.SortFunc(w.entries[first:end], ordered)
	}

	fields := descriptor.Fields()
	oneofs := len(w.oneofs)
	if count := descriptor.Oneofs().Len(); count > 0 {
		defer func() { w.oneofs = w.oneofs[:oneofs] }()
		w.settleOneofs(fields, count, first, end)
	}

	for from := first; from < end; {
		num := w.entries[from].num
		to := from + 1
		for to < end && w.entries[to].num == num {
			to++
		}
		if field := fields.ByNumber(num); field != nil {
			if err := w.field(field, from, to, oneofs, depth); err != nil {
				return err
			}
		}
		from = to
	}
	return nil
}

// settleOneofs works out which member of every oneof was set last
func (w *walker) settleOneofs(fields protoreflect.FieldDescriptors, count int, first, end int) {
	for i := 0; i < count; i++ {
		w.oneofs = append(w.oneofs, oneofState{winnerSeq: -1, otherSeq: -1})
	}
	states := w.oneofs[len(w.oneofs)-count:]

	for from := first; from < end; {
		num := w.entries[from].num
		to := from + 1
		for to < end && w.entries[to].num == num {
			to++
		}
		if field := fields.ByNumber(num); field != nil && field.ContainingOneof() != nil {
			// the last entry that really is this field
			last := int32(-1)
			for i := to - 1; i >= from; i-- {
				if w.entries[i].typ == wireType(field.Kind()) {
					last = w.entries[i].seq
					break
				}
			}
			if state := &states[field.ContainingOneof().Index()]; last > state.winnerSeq {
				state.otherSeq = max(state.otherSeq, state.winnerSeq)
				state.winner, state.winnerSeq = num, last
			} else {
				state.otherSeq = max(state.otherSeq, last)
			}
		}
		from = to
	}
}

// field prints one field, whose entries are the ones from from to to
func (w *walker) field(field protoreflect.FieldDescriptor, from, to int, oneofs int, depth int) error {
	kind := field.Kind()
	expected := wireType(kind)

	switch {
	case field.IsMap():
		return w.mapField(field, from, to, depth)

	case field.IsList():
		for i := from; i < to; i++ {
			entry := w.entries[i]
			switch {
			case entry.typ == expected:
				if kind == protoreflect.MessageKind {
					if err := w.nested(field, i, i+1, -1, depth); err != nil {
						return err
					}
					continue
				}
				if err := w.scalar(field, entry.bits, entry.data); err != nil {
					return err
				}
			case entry.typ == protowire.BytesType && expected != protowire.BytesType:
				if err := w.packed(field, expected, entry.data); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// what was set before another member of the same oneof is gone
	after := int32(-1)
	loser := false
	if oneof := field.ContainingOneof(); oneof != nil {
		state := w.oneofs[oneofs+oneof.Index()]
		loser = state.winner != field.Number()
		after = state.otherSeq
	}

	if kind == protoreflect.MessageKind {
		if loser {
			after = math.MaxInt32
		}
		return w.nested(field, from, to, after, depth)
	}

	// the last value counts, but all of them have to be valid
	last := -1
	for i := from; i < to; i++ {
		if w.entries[i].typ != expected {
			continue
		}
		if kind == protoreflect.StringKind && !utf8.Valid(w.entries[i].data) {
			return errMalformed
		}
		last = i
	}
	if last < 0 || loser {
		return nil
	}
	entry := w.entries[last]
	if !field.HasPresence() && isZero(kind, entry.bits, entry.data) {
		return nil
	}
	return w.scalar(field, entry.bits, entry.data)
}

// nested prints a message field. Its entries from from to to are the pieces of one message if
// the field isn't repeated. Pieces up to the position `after` were overwritten: they are only
// checked, not printed
func (w *walker) nested(field protoreflect.FieldDescriptor, from, to int, after int32, depth int) error {
	first := len(w.entries)
	seq := int32(0)
	found := false
	for i := from; i < to; i++ {
		entry := w.entries[i]
		if entry.typ != protowire.BytesType {
			continue
		}
		if entry.seq <= after {
			if err := w.check(field.Message(), entry.data, depth); err != nil {
				return err
			}
			continue
		}

		found = true
		var err error
		if seq, err = w.scan(entry.data, seq); err != nil {
			return err
		}
	}
	if !found {
		w.entries = w.entries[:first]
		return nil
	}

	w.out = append(w.out, field.TextName()...)
	w.out = append(w.out, ' ', '{', ' ')
	if err := w.message(field.Message(), first, depth+1); err != nil {
		return err
	}
	w.out = append(w.out, '}', ' ')
	return nil
}

// check tells whether bytes are a valid message, without printing it
func (w *walker) check(descriptor protoreflect.MessageDescriptor, data []byte, depth int) error {
	out, first := len(w.out), len(w.entries)
	_, err := w.scan(data, 0)
	if err == nil {
		err = w.message(descriptor, first, depth+1)
	}
	w.out, w.entries = w.out[:out], w.entries[:first]
	return err
}

// packed prints a repeated field's values that came packed into one entry
func (w *walker) packed(field protoreflect.FieldDescriptor, expected protowire.Type, data []byte) error {
	for len(data) > 0 {
		var bits uint64
		var size int
		switch expected {
		case protowire.VarintType:
			bits, size = protowire.ConsumeVarint(data)
		case protowire.Fixed32Type:
			var value uint32
			value, size = protowire.ConsumeFixed32(data)
			bits = uint64(value)
		case protowire.Fixed64Type:
			bits, size = protowire.ConsumeFixed64(data)
		default:
			return errUnsupported
		}
		if size < 0 {
			return errMalformed
		}
		data = data[size:]
		if err := w.scalar(field, bits, nil); err != nil {
			return err
		}
	}
	return nil
}

// mapField prints a map, sorted by its keys. On the wire a map is a repeated message of a key
// and a value, and for a key that comes more than once the last one counts
func (w *walker) mapField(field protoreflect.FieldDescriptor, from, to int, depth int) error {
	key, value := field.MapKey(), field.MapValue()
	keyKind, valueKind := key.Kind(), value.Kind()
	keyType, valueType := wireType(keyKind), wireType(valueKind)

	first := len(w.maps)
	defer func() { w.maps = w.maps[:first] }()

	for i := from; i < to; i++ {
		if w.entries[i].typ != protowire.BytesType {
			continue
		}
		data := w.entries[i].data
		entry := mapEntry{seq: w.entries[i].seq}
		for len(data) > 0 {
			num, typ, size := protowire.ConsumeTag(data)
			if size < 0 || num > protowire.MaxValidNumber {
				return errMalformed
			}
			data = data[size:]

			var bits uint64
			var payload []byte
			switch typ {
			case protowire.VarintType:
				bits, size = protowire.ConsumeVarint(data)
			case protowire.Fixed32Type:
				var fixed uint32
				fixed, size = protowire.ConsumeFixed32(data)
				bits = uint64(fixed)
			case protowire.Fixed64Type:
				bits, size = protowire.ConsumeFixed64(data)
			case protowire.BytesType:
				payload, size = protowire.ConsumeBytes(data)
			default:
				size = protowire.ConsumeFieldValue(num, typ, data)
			}
			if size < 0 {
				return errMalformed
			}
			data = data[size:]

			switch {
			case num == 1 && typ == keyType:
				if keyKind == protoreflect.StringKind && !utf8.Valid(payload) {
					return errMalformed
				}
				entry.keyBits, entry.keyData = bits, payload
			case num == 2 && typ == valueType:
				if valueKind == protoreflect.StringKind && !utf8.Valid(payload) {
					return errMalformed
				}
				if valueKind == protoreflect.MessageKind && entry.hasValue {
					// a value in pieces. Nothing writes that, the decoder can have it
					return errUnsupported
				}
				entry.valueBits, entry.valueData, entry.hasValue = bits, payload, true
			}
		}
		w.maps = append(w.maps, entry)
	}

	entries := w.maps[first:]
	slices.SortStableFunc(entries, func(a, b mapEntry) int {
		switch keyKind {
		case protoreflect.StringKind:
			return bytes.Compare(a.keyData, b.keyData)
		case protoreflect.BoolKind:
			return cmp.Compare(min(a.keyBits, 1), min(b.keyBits, 1))
		case protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
			return cmp.Compare(unsignedValue(keyKind, a.keyBits), unsignedValue(keyKind, b.keyBits))
		}
		return cmp.Compare(signedValue(keyKind, a.keyBits), signedValue(keyKind, b.keyBits))
	})
	same := func(a, b mapEntry) bool {
		switch keyKind {
		case protoreflect.StringKind:
			return bytes.Equal(a.keyData, b.keyData)
		case protoreflect.BoolKind:
			return (a.keyBits != 0) == (b.keyBits != 0)
		case protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
			return unsignedValue(keyKind, a.keyBits) == unsignedValue(keyKind, b.keyBits)
		}
		return signedValue(keyKind, a.keyBits) == signedValue(keyKind, b.keyBits)
	}

	for i := first; i < len(w.maps); i++ {
		entry := w.maps[i]
		// sorting kept entries of the same key in their order, so the next one overwrote this
		if i+1 < len(w.maps) && same(entry, w.maps[i+1]) {
			if valueKind == protoreflect.MessageKind {
				if err := w.check(value.Message(), entry.valueData, depth); err != nil {
					return err
				}
			}
			continue
		}

		w.out = append(w.out, field.TextName()...)
		w.out = append(w.out, ' ', '{', ' ')
		// an entry is a message of its own, and zero values aren't set in a message
		if !isZero(keyKind, entry.keyBits, entry.keyData) {
			if err := w.scalar(key, entry.keyBits, entry.keyData); err != nil {
				return err
			}
		}
		if valueKind == protoreflect.MessageKind {
			nested := len(w.entries)
			if _, err := w.scan(entry.valueData, 0); err != nil {
				return err
			}
			w.out = append(w.out, value.TextName()...)
			w.out = append(w.out, ' ', '{', ' ')
			if err := w.message(value.Message(), nested, depth+1); err != nil {
				return err
			}
			w.out = append(w.out, '}', ' ')
		} else if !isZero(valueKind, entry.valueBits, entry.valueData) {
			if err := w.scalar(value, entry.valueBits, entry.valueData); err != nil {
				return err
			}
		}
		w.out = append(w.out, '}', ' ')
	}
	return nil
}

// scalar prints `name: value `
func (w *walker) scalar(field protoreflect.FieldDescriptor, bits uint64, data []byte) error {
	kind := field.Kind()
	if kind == protoreflect.StringKind && !utf8.Valid(data) {
		return errMalformed
	}

	out := append(w.out, field.TextName()...)
	out = append(out, ':', ' ')
	switch kind {
	case protoreflect.BoolKind:
		if bits != 0 {
			out = append(out, "true"...)
		} else {
			out = append(out, "false"...)
		}
	case protoreflect.EnumKind:
		out = appendEnum(out, field, protoreflect.EnumNumber(int32(bits)))
	case protoreflect.FloatKind:
		out = appendFloat(out, float64(math.Float32frombits(uint32(bits))), 32)
	case protoreflect.DoubleKind:
		out = appendFloat(out, math.Float64frombits(bits), 64)
	case protoreflect.StringKind:
		out = append(out, '"')
		out = appendEscapedText(out, unsafe.String(unsafe.SliceData(data), len(data)))
		out = append(out, '"')
	case protoreflect.BytesKind:
		out = append(out, '"')
		out = AppendEscapedBytes(out, data)
		out = append(out, '"')
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		out = strconv.AppendUint(out, unsignedValue(kind, bits), 10)
	default:
		out = strconv.AppendInt(out, signedValue(kind, bits), 10)
	}
	w.out = append(out, ' ')
	return nil
}

// signedValue reads what is on the wire the way a signed integer field does
func signedValue(kind protoreflect.Kind, bits uint64) int64 {
	switch kind {
	case protoreflect.Int32Kind, protoreflect.Sfixed32Kind, protoreflect.EnumKind:
		return int64(int32(bits))
	case protoreflect.Sint32Kind:
		return int64(int32(protowire.DecodeZigZag(bits & math.MaxUint32)))
	case protoreflect.Sint64Kind:
		return protowire.DecodeZigZag(bits)
	}
	return int64(bits)
}

func unsignedValue(kind protoreflect.Kind, bits uint64) uint64 {
	if kind == protoreflect.Uint32Kind || kind == protoreflect.Fixed32Kind {
		return uint64(uint32(bits))
	}
	return bits
}

// isZero tells whether a value is its type's zero value, which proto3 treats as not set
func isZero(kind protoreflect.Kind, bits uint64, data []byte) bool {
	switch kind {
	case protoreflect.StringKind, protoreflect.BytesKind:
		return len(data) == 0
	case protoreflect.BoolKind, protoreflect.DoubleKind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		// a negative zero is a float that is set
		return bits == 0
	case protoreflect.FloatKind, protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return uint32(bits) == 0
	}
	return signedValue(kind, bits) == 0
}

// wireType is how a field of a kind is written. Anything else under its number isn't the field
func wireType(kind protoreflect.Kind) protowire.Type {
	switch kind {
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
		return protowire.Fixed32Type
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
		return protowire.Fixed64Type
	case protoreflect.StringKind, protoreflect.BytesKind, protoreflect.MessageKind:
		return protowire.BytesType
	case protoreflect.GroupKind:
		return protowire.StartGroupType
	}
	return protowire.VarintType
}

// lastVarint finds a varint field of a message without decoding anything else of it
func lastVarint(raw []byte, number protowire.Number) (value uint64) {
	for len(raw) > 0 {
		num, typ, size := protowire.ConsumeTag(raw)
		if size < 0 {
			return value
		}
		raw = raw[size:]
		if num == number && typ == protowire.VarintType {
			value, size = protowire.ConsumeVarint(raw)
		} else {
			size = protowire.ConsumeFieldValue(num, typ, raw)
		}
		if size < 0 {
			return value
		}
		raw = raw[size:]
	}
	return value
}

// lastBytes finds a length delimited field of a message without decoding anything else of it
func lastBytes(raw []byte, number protowire.Number) (value []byte) {
	for len(raw) > 0 {
		num, typ, size := protowire.ConsumeTag(raw)
		if size < 0 {
			return value
		}
		raw = raw[size:]
		if num == number && typ == protowire.BytesType {
			value, size = protowire.ConsumeBytes(raw)
		} else {
			size = protowire.ConsumeFieldValue(num, typ, raw)
		}
		if size < 0 {
			return value
		}
		raw = raw[size:]
	}
	return value
}

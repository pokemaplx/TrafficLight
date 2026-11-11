package web

import (
	"math/rand/v2"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/pokemaplx/TrafficLight/internal/decode"
	"github.com/pokemaplx/TrafficLight/internal/protos"
	"github.com/pokemaplx/TrafficLight/internal/traffic"
)

// mapObjects builds a GetMapObjects response the size of a real one, about 140 KB: cells full
// of forts and pokemon. That is what most of a scanner's traffic is made of
func mapObjects(tb testing.TB) []byte {
	tb.Helper()
	random := rand.New(rand.NewPCG(1, 2))
	message := dynamicpb.NewMessage(protos.Default().ResponseType(106))
	cells := message.Mutable(message.Descriptor().Fields().ByName("map_cell")).List()
	for i := 0; i < 21; i++ {
		cell := cells.AppendMutable().Message()
		for name, count := range map[string]int{"fort": 5, "wild_pokemon": 2, "catchable_pokemon": 2, "nearby_pokemon": 2} {
			list := cell.Mutable(cell.Descriptor().Fields().ByName(protoreflect.Name(name))).List()
			for j := 0; j < count; j++ {
				fill(random, list.AppendMutable().Message(), 2)
			}
		}
	}
	// fields in the order of their numbers, like every encoder but this one writes them
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		tb.Fatal(err)
	}
	return encoded
}

// fill sets every field of a message to something
func fill(random *rand.Rand, message protoreflect.Message, depth int) {
	fields := message.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		if field.IsMap() || field.ContainingOneof() != nil {
			continue
		}
		if field.IsList() {
			list := message.Mutable(field).List()
			if field.Message() == nil {
				list.Append(scalar(random, field))
			} else if depth > 0 {
				fill(random, list.AppendMutable().Message(), depth-1)
			}
			continue
		}
		if field.Message() == nil {
			message.Set(field, scalar(random, field))
		} else if depth > 0 {
			fill(random, message.Mutable(field).Message(), depth-1)
		}
	}
}

func scalar(random *rand.Rand, field protoreflect.FieldDescriptor) protoreflect.Value {
	switch field.Kind() {
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(true)
	case protoreflect.EnumKind:
		values := field.Enum().Values()
		return protoreflect.ValueOfEnum(values.Get(random.IntN(values.Len())).Number())
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(random.Int32())
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(random.Int64())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(random.Uint32())
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return protoreflect.ValueOfUint64(random.Uint64())
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(random.Float32() * 180)
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(random.Float64() * 180)
	case protoreflect.BytesKind:
		return protoreflect.ValueOfBytes([]byte("0123456789ab"))
	}
	return protoreflect.ValueOfString("0123456789abcdef0123")
}

// What listing one map objects response for a browser costs: decoding it and writing its text
func BenchmarkListRecord(b *testing.B) {
	s := server(10)
	record := newRecord(traffic.Request{Protos: []traffic.RawProto{{Method: 106, Response: mapObjects(b)}}})
	var buffer []byte
	b.SetBytes(int64(len(record.Protos[0].Response)))
	b.ReportAllocs()
	for b.Loop() {
		buffer = s.appendRecord(buffer[:0], record)
	}
}

// What inspecting it costs
func BenchmarkInspectRecord(b *testing.B) {
	registry := protos.Default()
	raw := traffic.RawProto{Method: 106, Response: mapObjects(b)}
	var buffer []byte
	b.SetBytes(int64(len(raw.Response)))
	b.ReportAllocs()
	for b.Loop() {
		buffer = decode.AppendDetail(buffer[:0], decode.Decode(registry, raw))
	}
}

// What keeping it costs
func BenchmarkAddRecord(b *testing.B) {
	s := New(Options{MaxRecords: 5000, MaxBytes: 64 << 20})
	payload := mapObjects(b)
	b.ReportAllocs()
	for b.Loop() {
		s.Add(traffic.Request{Protos: []traffic.RawProto{{Method: 106, Response: payload}}})
	}
}

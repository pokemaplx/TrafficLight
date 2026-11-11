package decode

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/pokemaplx/TrafficLight/internal/protos"
	"github.com/pokemaplx/TrafficLight/internal/traffic"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type sample struct {
	descriptor protoreflect.MessageDescriptor
	raw        []byte
}

// samples are the messages of the golden file, with the type each of them is
func samples(tb testing.TB) []sample {
	tb.Helper()
	file, err := os.Open("testdata/golden.jsonl.gz")
	if err != nil {
		tb.Fatal(err)
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		tb.Fatal(err)
	}

	registry := protos.Default()
	var out []sample
	lines := bufio.NewScanner(unzipped)
	lines.Buffer(nil, 64<<20)
	for lines.Scan() {
		var golden struct {
			Method   int64
			Request  string
			Response string
		}
		if err := json.Unmarshal(lines.Bytes(), &golden); err != nil {
			tb.Fatal(err)
		}
		_, method := registry.Resolve(golden.Method)
		requestType, responseType := registry.Types(method)
		request, _ := base64.StdEncoding.DecodeString(golden.Request)
		response, _ := base64.StdEncoding.DecodeString(golden.Response)
		if descriptor := requestType; descriptor != nil {
			out = append(out, sample{descriptor, request})
		}
		if descriptor := responseType; descriptor != nil {
			out = append(out, sample{descriptor, response})
		}
	}
	if len(out) == 0 {
		tb.Fatal("no samples")
	}
	return out
}

// agree fails if the walker says anything else about a message than decoding and printing it
func agree(t *testing.T, w *walker, descriptor protoreflect.MessageDescriptor, raw []byte, what string) (unsupported bool) {
	t.Helper()
	decoded := decodeMessage(descriptor, raw)

	w.out = w.out[:0]
	empty, err := w.text(descriptor, raw)
	switch {
	case err == errUnsupported:
		return true
	case err != nil:
		if decoded.Payload != nil {
			t.Fatalf("%s of %s: the walker refuses what the decoder takes\n%x", what, descriptor.Name(), raw)
		}
		return false
	case decoded.Payload == nil:
		t.Fatalf("%s of %s: the walker takes what the decoder refuses\n%x\n%s", what, descriptor.Name(), raw, w.out)
	}

	want := AppendText(nil, decoded.Payload, true)
	if !bytes.Equal(want, w.out) {
		t.Fatalf("%s of %s: the text differs\n%x\ndecoder: %s\nwalker:  %s", what, descriptor.Name(), raw, want, w.out)
	}
	if empty != isEmpty(decoded.Payload) {
		t.Fatalf("%s of %s: empty is %t for the walker", what, descriptor.Name(), empty)
	}
	return false
}

// topLevel splits a message into its fields
func topLevel(raw []byte) [][]byte {
	var fields [][]byte
	for len(raw) > 0 {
		_, _, size := protowire.ConsumeField(raw)
		if size < 0 {
			return nil
		}
		fields = append(fields, raw[:size])
		raw = raw[size:]
	}
	return fields
}

// Protobuf allows a lot that no encoder writes, and bytes on the wire can be anything. The
// walker has to make of all of it what the decoder makes of it.
func TestTheWalkerAgreesWithTheDecoder(t *testing.T) {
	all := samples(t)
	random := rand.New(rand.NewPCG(7, 11))
	w := new(walker)
	checked, unsupported := 0, 0
	check := func(descriptor protoreflect.MessageDescriptor, raw []byte, what string) {
		checked++
		if agree(t, w, descriptor, raw, what) {
			unsupported++
		}
	}

	for _, sample := range all {
		raw := sample.raw
		check(sample.descriptor, raw, "the message")
		if len(raw) == 0 {
			continue
		}

		// set twice: fields that aren't repeated are overwritten, messages merged, oneofs and
		// map keys settled by whichever came last
		check(sample.descriptor, append(append([]byte(nil), raw...), raw...), "the message twice")
		other := all[random.IntN(len(all))]
		check(sample.descriptor, append(append([]byte(nil), raw...), other.raw...), "the message and another")
		check(other.descriptor, raw, "the message read as another type")

		fields := topLevel(raw)
		for round := 0; round < 4; round++ {
			// fields in any order, some of them more than once, some missing
			var shuffled []byte
			for _, i := range random.Perm(len(fields)) {
				switch random.IntN(6) {
				case 0:
				case 1:
					shuffled = append(append(shuffled, fields[i]...), fields[i]...)
				default:
					shuffled = append(shuffled, fields[i]...)
				}
			}
			check(sample.descriptor, shuffled, "shuffled fields")
		}

		for round := 0; round < 40; round++ {
			broken := append([]byte(nil), raw...)
			switch random.IntN(4) {
			case 0:
				broken = broken[:random.IntN(len(broken))]
			case 1:
				broken[random.IntN(len(broken))] = byte(random.IntN(256))
			case 2:
				for flips := 1 + random.IntN(4); flips > 0; flips-- {
					broken[random.IntN(len(broken))] ^= 1 << random.IntN(8)
				}
			default:
				// a field of a random number and type somewhere in between
				at := random.IntN(len(broken) + 1)
				extra := protowire.AppendTag(nil, protowire.Number(1+random.IntN(40)), protowire.Type(random.IntN(6)))
				extra = append(extra, byte(random.IntN(256)), byte(random.IntN(4)), byte(random.IntN(256)))
				broken = append(broken[:at:at], append(extra, broken[at:]...)...)
			}
			check(sample.descriptor, broken, "broken bytes")
		}
	}

	// the decoder has to stay the exception, or the walker wouldn't be worth having
	if unsupported*100 > checked {
		t.Errorf("%d of %d messages were left to the decoder", unsupported, checked)
	}
	t.Logf("%d messages compared, %d left to the decoder", checked, unsupported)
}

func FuzzWalker(f *testing.F) {
	all := samples(f)
	for i, sample := range all {
		if len(sample.raw) < 4096 {
			f.Add(uint16(i), sample.raw)
		}
	}
	f.Fuzz(func(t *testing.T, which uint16, raw []byte) {
		agree(t, new(walker), all[int(which)%len(all)].descriptor, raw, "fuzzed bytes")
	})
}

// a summary is the same whether the walker or the decoder wrote its text
func TestSummariesDoNotDependOnTheWalker(t *testing.T) {
	registry := protos.Default()
	// a map whose value comes in two pieces is something the walker leaves to the decoder
	var descriptor protoreflect.MessageDescriptor
	var mapField protoreflect.FieldDescriptor
	var method int32
	for number := int32(0); number < 10000; number++ {
		candidate := registry.ResponseType(number)
		if candidate == nil {
			continue
		}
		fields := candidate.Fields()
		for i := 0; i < fields.Len(); i++ {
			if field := fields.Get(i); field.IsMap() && field.MapValue().Message() != nil && field.MapKey().Kind() == protoreflect.StringKind {
				descriptor, mapField, method = candidate, field, number
			}
		}
	}
	if descriptor == nil {
		t.Skip("no response has a map of messages")
	}

	entry := protowire.AppendTag(nil, 1, protowire.BytesType)
	entry = protowire.AppendString(entry, "key")
	entry = protowire.AppendTag(entry, 2, protowire.BytesType)
	entry = protowire.AppendBytes(entry, nil)
	entry = protowire.AppendTag(entry, 2, protowire.BytesType)
	entry = protowire.AppendBytes(entry, nil)
	raw := protowire.AppendTag(nil, mapField.Number(), protowire.BytesType)
	raw = protowire.AppendBytes(raw, entry)

	if _, err := new(walker).text(descriptor, raw); err != errUnsupported {
		t.Fatalf("the walker was expected to leave this to the decoder, it said %v", err)
	}
	var summary struct {
		Response struct{ State, Text string }
	}
	if err := json.Unmarshal(AppendSummary(nil, registry, traffic.RawProto{Method: int64(method), Response: raw}), &summary); err != nil {
		t.Fatal(err)
	}
	want := string(AppendText(nil, decodeMessage(descriptor, raw).Payload, true))
	if summary.Response.State != "ok" || summary.Response.Text != want {
		t.Errorf("got %q (%s), want %q", summary.Response.Text, summary.Response.State, want)
	}
}

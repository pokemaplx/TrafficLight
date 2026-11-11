package pogo

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// testdata/golden.jsonl.gz holds what the Python implementation Traffic Light used to be made
// of protos of every kind: what it listed, what its inspector showed and what it exported.
// The web UI was written against that output, so this one has to say exactly the same.
//
// TRAFFICLIGHT_GOLDEN points at another file of the same shape, to compare more cases.
func TestMatchesThePythonImplementation(t *testing.T) {
	path := "testdata/golden.jsonl.gz"
	if other := os.Getenv("TRAFFICLIGHT_GOLDEN"); other != "" {
		path = other
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var reader io.Reader = file
	if strings.HasSuffix(path, ".gz") {
		unzipped, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		reader = unzipped
	}

	registry := Default()
	lines := bufio.NewReaderSize(reader, 1<<20)
	cases, failures := 0, 0
	for {
		line, err := lines.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			cases++
			if !compareCase(t, registry, cases, line) {
				failures++
				if failures >= 10 {
					t.Fatal("stopping after 10 cases that differ")
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if cases == 0 {
		t.Fatal("no cases in " + path)
	}
	t.Logf("%d cases compared", cases)
}

func compareCase(t *testing.T, registry *Registry, number int, line []byte) bool {
	var golden struct {
		Method   int64
		Request  string
		Response string
		Summary  json.RawMessage
		Detail   json.RawMessage
		Export   json.RawMessage
	}
	if err := json.Unmarshal(line, &golden); err != nil {
		t.Fatalf("case %d: %v", number, err)
	}
	request, _ := base64.StdEncoding.DecodeString(golden.Request)
	response, _ := base64.StdEncoding.DecodeString(golden.Response)
	raw := RawProto{Method: golden.Method, Request: request, Response: response}
	proto := registry.Decode(raw)

	same := true
	for _, part := range []struct {
		name string
		want json.RawMessage
		got  []byte
	}{
		{"summary", golden.Summary, registry.AppendSummary(nil, raw)},
		{"detail", golden.Detail, AppendDetail(nil, proto)},
		{"export", golden.Export, AppendExport(nil, proto)},
	} {
		want, got := parse(t, part.want), parse(t, part.got)
		if reflect.DeepEqual(want, got) {
			continue
		}
		same = false
		t.Errorf("case %d (method %d, %s): the %s differs at %s", number, golden.Method, proto.Request.Name, part.name, firstDifference("", want, got))
	}
	return same
}

func parse(t *testing.T, data []byte) any {
	decoder := json.NewDecoder(bytes.NewReader(data))
	// numbers are compared as they are written: 0.9 and 0.8999999761581421 are not the same
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("invalid json: %v\n%.300s", err, data)
	}
	return value
}

// firstDifference says where two json values stop being the same
func firstDifference(path string, want, got any) string {
	switch want := want.(type) {
	case map[string]any:
		gotMap, ok := got.(map[string]any)
		if !ok {
			break
		}
		for key, value := range want {
			other, exists := gotMap[key]
			if !exists {
				return path + "." + key + ": missing"
			}
			if !reflect.DeepEqual(value, other) {
				return firstDifference(path+"."+key, value, other)
			}
		}
		for key := range gotMap {
			if _, exists := want[key]; !exists {
				return path + "." + key + ": unexpected"
			}
		}
	case []any:
		gotList, ok := got.([]any)
		if !ok {
			break
		}
		if len(want) != len(gotList) {
			return path + ": length differs"
		}
		for i := range want {
			if !reflect.DeepEqual(want[i], gotList[i]) {
				return firstDifference(path+"["+strconv.Itoa(i)+"]", want[i], gotList[i])
			}
		}
	}
	return path + ":\n  python: " + short(want) + "\n  go:     " + short(got)
}

func short(value any) string {
	text, _ := json.Marshal(value)
	if len(text) > 400 {
		return string(text[:400]) + "…"
	}
	return string(text)
}

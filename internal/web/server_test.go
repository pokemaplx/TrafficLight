package web

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/pokemaplx/TrafficLight/internal/pogo"
)

// The record keeping is the one place where a bug quietly throws away traffic the user asked
// to keep, or lets the log grow past what it was told to fit in.

func server(maxRecords int) *Server {
	return New(Options{MaxRecords: maxRecords})
}

func request(payload int) pogo.Request {
	return pogo.Request{RPCID: 1, RPCStatus: 1, Protos: []pogo.RawProto{{Method: 104, Response: make([]byte, payload)}}}
}

func add(s *Server, count int) {
	for i := 0; i < count; i++ {
		s.Add(request(0))
	}
}

func ids(s *Server) []uint64 {
	out := []uint64{}
	for _, record := range s.store.snapshot() {
		out = append(out, record.ID)
	}
	return out
}

func savedIDs(s *Server) []uint64 {
	return s.store.savedIDs()
}

// connect stands in for a browser: a client without a connection, whose queue is read instead
func connect(s *Server) *client {
	client := newClient(nil)
	s.clients[client] = struct{}{}
	return client
}

// messages renders what is queued for a client into what a browser would get
func messages(t *testing.T, s *Server, c *client) []map[string]any {
	t.Helper()
	var out []map[string]any
	err := s.render(c.take(), func(message []byte) error {
		var parsed map[string]any
		if err := json.Unmarshal(message, &parsed); err != nil {
			t.Fatalf("a message isn't json: %v\n%s", err, message)
		}
		out = append(out, parsed)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func command(s *Server, from *client, payload string) {
	if from == nil {
		from = newClient(nil)
	}
	s.handleCommand(from, []byte(payload))
}

func expect(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func recordIDs(message map[string]any) []uint64 {
	out := []uint64{}
	records, _ := message["records"].([]any)
	for _, record := range records {
		out = append(out, uint64(record.(map[string]any)["id"].(float64)))
	}
	return out
}

// ---------------------------------------------------------------- eviction

func TestEvictsOldestWhenNothingIsSaved(t *testing.T) {
	s := server(3)
	add(s, 5)
	expect(t, "drops the oldest first and keeps the cap", ids(s), []uint64{3, 4, 5})
}

func TestSavedRecordsSurviveEviction(t *testing.T) {
	s := server(3)
	add(s, 3)
	command(s, nil, `{"type":"save","id":1,"value":true}`)
	add(s, 3)
	expect(t, "a saved record is kept past the cap, the oldest unsaved went instead", ids(s), []uint64{1, 5, 6})
}

func TestAddReportsWhatItDropped(t *testing.T) {
	s := server(2)
	add(s, 2)
	browser := connect(s)
	add(s, 1)
	got := messages(t, s, browser)
	if len(got) != 1 {
		t.Fatalf("expected one message, got %d", len(got))
	}
	expect(t, "the browser is told what went", got[0]["dropped"], []any{float64(1)})
	expect(t, "and what came", recordIDs(got[0]), []uint64{3})
}

func TestRecordsThatPiledUpGoOutTogether(t *testing.T) {
	s := server(2)
	add(s, 2)
	browser := connect(s)
	add(s, 3)
	got := messages(t, s, browser)
	if len(got) != 1 {
		t.Fatalf("expected one message, got %d", len(got))
	}
	expect(t, "every record is in it", recordIDs(got[0]), []uint64{3, 4, 5})
	expect(t, "and everything that was dropped", got[0]["dropped"], []any{float64(1), float64(2), float64(3)})
}

func TestSavingNeverPushesTheLogOverTheCap(t *testing.T) {
	s := server(2)
	add(s, 2)
	command(s, nil, `{"type":"save","id":1,"value":true}`)
	// the second one is refused: at most half of a two record log can be saved
	command(s, nil, `{"type":"save","id":2,"value":true}`)
	expect(t, "the save limit leaves room to breathe", savedIDs(s), []uint64{1})

	add(s, 2)
	expect(t, "still at the cap", ids(s), []uint64{1, 4})
	command(s, nil, `{"type":"save","id":1,"value":false}`)
	add(s, 1)
	expect(t, "unsaved, it goes like any other", ids(s), []uint64{4, 5})
}

func TestSaveLimit(t *testing.T) {
	s := server(1000)
	add(s, saveLimit+5)
	for id := 1; id <= saveLimit+1; id++ {
		command(s, nil, `{"type":"save","id":`+itoa(id)+`,"value":true}`)
	}
	expect(t, "refuses past the limit", len(savedIDs(s)), saveLimit)

	browser := newClient(nil)
	command(s, browser, `{"type":"save","id":`+itoa(saveLimit+3)+`,"value":true}`)
	got := messages(t, s, browser)
	if len(got) != 1 || got[0]["type"] != "notice" || !strings.Contains(strings.ToLower(got[0]["text"].(string)), "save") {
		t.Errorf("it doesn't say why: %v", got)
	}
}

func TestSaveLimitFollowsASmallLog(t *testing.T) {
	s := server(10)
	add(s, 10)
	for id := 1; id <= 10; id++ {
		command(s, nil, `{"type":"save","id":`+itoa(id)+`,"value":true}`)
	}
	expect(t, "never saves more than half a small log", len(savedIDs(s)), 5)
}

func TestUnknownSaveIDsAreIgnored(t *testing.T) {
	s := server(5)
	add(s, 2)
	command(s, nil, `{"type":"save","id":99,"value":true}`)
	command(s, nil, `{"type":"save","id":"nonsense","value":true}`)
	command(s, nil, `{"type":"save","value":true}`)
	command(s, nil, `not even json`)
	expect(t, "ignores ids that are not in the log", savedIDs(s), []uint64{})
}

// ---------------------------------------------------------------- memory

func TestTheLogStaysWithinItsMemory(t *testing.T) {
	// room for about four records of 1000 bytes each
	s := New(Options{MaxRecords: 1000, MaxBytes: 5000})
	for i := 0; i < 50; i++ {
		s.Add(request(1000))
	}
	if s.store.bytes > 5000 {
		t.Errorf("the log weighs %d bytes, more than its limit", s.store.bytes)
	}
	expect(t, "the newest records are the ones kept", ids(s), []uint64{47, 48, 49, 50})
}

func TestARecordBiggerThanTheLogIsStillTaken(t *testing.T) {
	s := New(Options{MaxRecords: 1000, MaxBytes: 5000})
	add(s, 3)
	s.Add(request(50_000))
	expect(t, "it pushes everything else out", ids(s), []uint64{4})
	add(s, 1)
	expect(t, "and goes with the next one", ids(s), []uint64{5})
}

func TestDroppedRecordsFreeTheirMemory(t *testing.T) {
	s := New(Options{MaxRecords: 10, MaxBytes: 0})
	for i := 0; i < 5000; i++ {
		s.Add(request(100))
	}
	if len(s.store.records) > 1024 {
		t.Errorf("the list still has %d slots for a log of 10", len(s.store.records))
	}
	expect(t, "the weight is that of what is left", s.store.bytes, int64(10*(recordOverhead+protoOverhead+100)))
	if s.store.get(4995) == nil || s.store.get(4990) != nil {
		t.Error("records aren't found by their id after others were dropped")
	}
}

func TestSavedRecordsLeaveHalfOfTheMemory(t *testing.T) {
	s := New(Options{MaxRecords: 1000, MaxBytes: 10_000})
	for i := 0; i < 6; i++ {
		s.Add(request(1000))
	}
	browser := newClient(nil)
	for id := 1; id <= 6; id++ {
		command(s, browser, `{"type":"save","id":`+itoa(id)+`,"value":true}`)
	}
	if s.store.savedBytes > 5000 {
		t.Errorf("saved records weigh %d bytes, more than half of the log", s.store.savedBytes)
	}
	notices := 0
	for _, message := range messages(t, s, browser) {
		if message["type"] == "notice" {
			notices++
		}
	}
	if notices == 0 {
		t.Error("refusing to save doesn't say why")
	}

	for i := 0; i < 20; i++ {
		s.Add(request(1000))
	}
	for _, id := range savedIDs(s) {
		if s.store.get(id) == nil {
			t.Errorf("saved record %d was dropped", id)
		}
	}
	if s.store.bytes > 10_000 {
		t.Errorf("the log weighs %d bytes, more than its limit", s.store.bytes)
	}
}

// ---------------------------------------------------------------- clear

func TestClearKeepsSavedRecords(t *testing.T) {
	s := server(10)
	add(s, 4)
	command(s, nil, `{"type":"save","id":2,"value":true}`)
	browser := connect(s)
	command(s, nil, `{"type":"clear"}`)
	expect(t, "only the saved one is left", ids(s), []uint64{2})

	got := messages(t, s, browser)
	kinds := []any{}
	for _, message := range got {
		kinds = append(kinds, message["type"])
	}
	expect(t, "the survivors ride on the clear, not on a history after it", kinds, []any{"clear", "saved"})
	expect(t, "and they are all there", recordIDs(got[0]), []uint64{2})
	expect(t, "still marked as saved", got[1]["ids"], []any{float64(2)})
}

func TestClearWithSavedEmptiesEverything(t *testing.T) {
	s := server(10)
	add(s, 3)
	command(s, nil, `{"type":"save","id":2,"value":true}`)
	browser := connect(s)
	command(s, nil, `{"type":"clear","saved":true}`)
	expect(t, "nothing is left", ids(s), []uint64{})
	expect(t, "nothing is saved", savedIDs(s), []uint64{})
	expect(t, "nothing weighs anything", s.store.bytes, int64(0))

	got := messages(t, s, browser)
	expect(t, "an empty list is still there", got[0]["records"], []any{})
	expect(t, "and browsers are told the saved ones went", got[len(got)-1]["ids"], []any{})
}

func TestRecordIDsNeverRestart(t *testing.T) {
	s := server(10)
	add(s, 3)
	command(s, nil, `{"type":"clear","saved":true}`)
	add(s, 1)
	expect(t, "ids keep counting, so cached details stay valid", ids(s), []uint64{4})
}

// ---------------------------------------------------------------- the rest of the protocol

func TestPauseBlocksRecords(t *testing.T) {
	s := server(10)
	browser := connect(s)
	command(s, nil, `{"type":"pause","value":true}`)
	add(s, 3)
	expect(t, "paused drops incoming traffic", ids(s), []uint64{})
	command(s, nil, `{"type":"pause","value":false}`)
	add(s, 1)
	expect(t, "resuming takes it again", len(ids(s)), 1)

	got := messages(t, s, browser)
	expect(t, "browsers are told about pausing", got[0], map[string]any{"type": "state", "paused": true})
	expect(t, "and about resuming", got[1], map[string]any{"type": "state", "paused": false})
}

func TestHelloCarriesTheSavedIDs(t *testing.T) {
	s := server(10)
	add(s, 2)
	command(s, nil, `{"type":"save","id":2,"value":true}`)
	var hello map[string]any
	if err := json.Unmarshal(s.hello(), &hello); err != nil {
		t.Fatal(err)
	}
	expect(t, "hello lists the saved ids", hello["saved"], []any{float64(2)})
	expect(t, "and the cap", hello["max_records"], float64(10))
}

func TestHistoryIsChunked(t *testing.T) {
	s := server(1200)
	add(s, 1200)
	browser := newClient(nil)
	browser.push(event{kind: eventHistory, records: s.store.snapshot()})
	got := messages(t, s, browser)
	expect(t, "history comes in chunks", len(got), 3)

	var all []uint64
	for _, message := range got {
		expect(t, "of history", message["type"], "history")
		all = append(all, recordIDs(message)...)
	}
	expect(t, "without losing any", len(all), 1200)
	for i, id := range all {
		if id != uint64(i+1) {
			t.Fatalf("the history is out of order at %d: %d", i, id)
		}
	}
}

func TestHistoryChunksFollowTheWeightOfTheirRecords(t *testing.T) {
	s := server(100)
	for i := 0; i < 20; i++ {
		s.Add(request(chunkBytes / 4))
	}
	browser := newClient(nil)
	browser.push(event{kind: eventHistory, records: s.store.snapshot()})
	got := messages(t, s, browser)
	expect(t, "big records are spread over more messages", len(got), 5)
}

func TestARecordIsListedTheWayBrowsersExpect(t *testing.T) {
	s := server(10)
	browser := connect(s)
	handle := int64(7)
	s.Add(pogo.Request{RPCID: 3, RPCStatus: 1, RPCHandle: &handle, Protos: []pogo.RawProto{{Method: 104}}})
	record := messages(t, s, browser)[0]["records"].([]any)[0].(map[string]any)

	expect(t, "id", record["id"], float64(1))
	expect(t, "rpc id", record["rpc_id"], float64(3))
	expect(t, "rpc status", record["rpc_status"], float64(1))
	expect(t, "rpc handle", record["rpc_handle"], float64(7))
	proto := record["protos"].([]any)[0].(map[string]any)
	expect(t, "method", proto["method"], "METHOD_FORT_DETAILS")
	expect(t, "request", proto["request"], map[string]any{"name": "FortDetailsProto", "state": "empty", "size": float64(0), "text": ""})
}

func TestABrowserThatFallsBehindIsDropped(t *testing.T) {
	s := server(10)
	browser := connect(s)
	add(s, maxClientBacklog+10)
	if _, still := s.clients[browser]; still {
		t.Error("a browser that never reads is kept forever")
	}
	if len(browser.queue) > maxClientBacklog+1 {
		t.Errorf("its queue grew to %d events", len(browser.queue))
	}
}

func itoa(i int) string {
	text, _ := json.Marshal(i)
	return string(text)
}

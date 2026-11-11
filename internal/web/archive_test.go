package web

import (
	"errors"
	"sort"
	"testing"

	"github.com/pokemaplx/TrafficLight/internal/pogo"
)

// memoryArchive stands in for a database
type memoryArchive struct {
	saved   map[int64]Archived
	nextKey int64
	// makes every call fail, like a database that went away
	down bool
}

func newMemoryArchive() *memoryArchive {
	return &memoryArchive{saved: map[int64]Archived{}, nextKey: 1}
}

var errDown = errors.New("the database is down")

func (a *memoryArchive) Load() ([]Archived, error) {
	if a.down {
		return nil, errDown
	}
	var out []Archived
	for _, saved := range a.saved {
		out = append(out, saved)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (a *memoryArchive) Save(saved Archived) (int64, error) {
	if a.down {
		return 0, errDown
	}
	saved.Key = a.nextKey
	a.nextKey++
	a.saved[saved.Key] = saved
	return saved.Key, nil
}

func (a *memoryArchive) Delete(keys []int64) error {
	if a.down {
		return errDown
	}
	for _, key := range keys {
		delete(a.saved, key)
	}
	return nil
}

func archived(t *testing.T, maxRecords int) (*Server, *memoryArchive) {
	t.Helper()
	archive := newMemoryArchive()
	return New(Options{MaxRecords: maxRecords, Archive: archive}), archive
}

func notices(t *testing.T, s *Server, c *client) int {
	t.Helper()
	count := 0
	for _, message := range messages(t, s, c) {
		if message["type"] == "notice" {
			count++
		}
	}
	return count
}

func TestSavingWritesToTheArchive(t *testing.T) {
	s, archive := archived(t, 10)
	handle := int64(7)
	s.Add(pogo.Request{RPCID: 3, RPCStatus: 1, RPCHandle: &handle, Protos: []pogo.RawProto{{Method: 104, Request: []byte("abc")}}})
	add(s, 2)

	command(s, nil, `{"type":"save","id":1,"value":true}`)
	if len(archive.saved) != 1 {
		t.Fatalf("the archive holds %d requests", len(archive.saved))
	}
	saved := archive.saved[1]
	if saved.Request.RPCID != 3 || *saved.Request.RPCHandle != 7 || string(saved.Request.Protos[0].Request) != "abc" || saved.Time != s.store.get(1).Time {
		t.Errorf("it holds %+v", saved)
	}

	// saving what is saved already doesn't store it twice
	command(s, nil, `{"type":"save","id":1,"value":true}`)
	expect(t, "saved once", len(archive.saved), 1)

	command(s, nil, `{"type":"save","id":1,"value":false}`)
	expect(t, "unsaving deletes it", len(archive.saved), 0)
	expect(t, "and unsaves it", savedIDs(s), []uint64{})
}

func TestSavedRequestsComeBackAfterARestart(t *testing.T) {
	s, archive := archived(t, 10)
	add(s, 3)
	command(s, nil, `{"type":"save","id":1,"value":true}`)
	command(s, nil, `{"type":"save","id":3,"value":true}`)
	times := []int64{s.store.get(1).Time, s.store.get(3).Time}

	// a new server on the same archive, like the same database after a restart
	restarted := New(Options{MaxRecords: 3, Archive: archive})
	count, err := restarted.Restore()
	if err != nil || count != 2 {
		t.Fatalf("restored %d, %v", count, err)
	}
	expect(t, "they are in the log again", ids(restarted), []uint64{1, 2})
	expect(t, "and saved", savedIDs(restarted), []uint64{1, 2})
	expect(t, "with the time they arrived at", []int64{restarted.store.get(1).Time, restarted.store.get(2).Time}, times)

	add(restarted, 5)
	expect(t, "new traffic doesn't drop them", ids(restarted), []uint64{1, 2, 7})

	// they are still the archive's: unsaving one deletes it there
	command(restarted, nil, `{"type":"save","id":1,"value":false}`)
	expect(t, "unsaving a restored request deletes it", len(archive.saved), 1)
}

func TestClearingOnlyTouchesTheArchiveWhenAskedTo(t *testing.T) {
	s, archive := archived(t, 10)
	add(s, 3)
	command(s, nil, `{"type":"save","id":2,"value":true}`)

	command(s, nil, `{"type":"clear"}`)
	expect(t, "a clear keeps what is saved", len(archive.saved), 1)
	expect(t, "in the log too", ids(s), []uint64{2})

	command(s, nil, `{"type":"clear","saved":true}`)
	expect(t, "clearing the saved ones deletes them", len(archive.saved), 0)
	expect(t, "in the log too", ids(s), []uint64{})
}

func TestADatabaseThatIsDownNeverLies(t *testing.T) {
	s, archive := archived(t, 10)
	add(s, 3)
	command(s, nil, `{"type":"save","id":1,"value":true}`)
	browser := connect(s)

	archive.down = true
	command(s, browser, `{"type":"save","id":2,"value":true}`)
	expect(t, "what the database didn't take is not shown as saved", savedIDs(s), []uint64{1})
	expect(t, "and the browser is told why", notices(t, s, browser), 1)

	command(s, browser, `{"type":"save","id":1,"value":false}`)
	expect(t, "what the database still has stays saved", savedIDs(s), []uint64{1})
	expect(t, "and the browser is told why", notices(t, s, browser), 1)

	command(s, browser, `{"type":"clear","saved":true}`)
	expect(t, "clearing leaves what couldn't be deleted", ids(s), []uint64{1})
	expect(t, "still saved", savedIDs(s), []uint64{1})

	if _, err := New(Options{MaxRecords: 10, Archive: archive}).Restore(); err == nil {
		t.Error("starting without the saved requests goes unnoticed")
	}

	archive.down = false
	command(s, browser, `{"type":"save","id":1,"value":false}`)
	expect(t, "it works again once the database is back", len(archive.saved), 0)
}

func TestWithoutAnArchiveNothingChanges(t *testing.T) {
	s := server(10)
	if count, err := s.Restore(); count != 0 || err != nil {
		t.Errorf("restored %d, %v", count, err)
	}
}

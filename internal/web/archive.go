package web

import (
	"fmt"
	"os"

	"github.com/pokemaplx/TrafficLight/internal/pogo"
)

// Archive keeps saved requests somewhere that outlives Traffic Light, i.e. a database.
//
// The log itself stays in memory: an archive only mirrors what is saved. A request is written
// to it when you save it, removed when you unsave it or clear the saved ones, and all of them
// are put back into the log when Traffic Light starts.
type Archive interface {
	// Load returns everything that is saved, oldest first
	Load() ([]Archived, error)
	// Save stores a request and returns the key it can be deleted by
	Save(saved Archived) (key int64, err error)
	Delete(keys []int64) error
}

// Archived is a saved request the way an archive holds it
type Archived struct {
	Key int64
	// when it arrived, in unix milliseconds
	Time    int64
	Request pogo.Request
}

// Restore puts what the archive holds back into the log, as saved requests. It is meant to be
// called once, before any traffic arrives, and returns how many there were
func (s *Server) Restore() (int, error) {
	if s.archive == nil {
		return 0, nil
	}
	saved, err := s.archive.Load()
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, archived := range saved {
		record := newRecord(archived.Request)
		record.Time = archived.Time
		record.key = archived.Key
		s.store.restore(record)
	}
	return len(saved), nil
}

// setSaved saves or unsaves a record and tells every browser. With an archive, the record is
// only saved once the archive has it, and only unsaved once the archive let go of it
func (s *Server) setSaved(from *client, id uint64, saved bool) {
	if s.archive != nil {
		// the archive is slow compared to the log, so it is never asked while the log's lock
		// is held. One change at a time keeps the two from disagreeing about a record
		s.archiving.Lock()
		defer s.archiving.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	record := s.store.get(id)
	if record == nil {
		return
	}

	if saved && !record.saved {
		// marked right away, so the record can't be dropped while the archive takes it
		switch s.store.setSaved(id, true) {
		case saveTooMany:
			from.push(notice(fmt.Sprintf("You can save at most %d requests", s.store.maxSaved())))
			return
		case saveTooBig:
			from.push(notice("Saved requests can take at most half of the log's memory"))
			return
		}
		if s.archive != nil {
			archived := Archived{Time: record.Time, Request: pogo.Request{
				RPCID:     record.RPCID,
				RPCStatus: record.RPCStatus,
				RPCHandle: record.RPCHandle,
				Protos:    record.Protos,
			}}
			s.mu.Unlock()
			key, err := s.archive.Save(archived)
			s.mu.Lock()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Saving a request to the database failed: %v\n", err)
				s.store.setSaved(id, false)
				from.push(notice("Couldn't save the request: the database didn't take it"))
				return
			}
			record.key = key
		}
	} else if !saved && record.saved {
		if s.archive != nil && record.key != 0 {
			key := record.key
			s.mu.Unlock()
			err := s.archive.Delete([]int64{key})
			s.mu.Lock()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Deleting a saved request from the database failed: %v\n", err)
				from.push(notice("Couldn't unsave the request: the database still has it"))
				return
			}
			record.key = 0
		}
		s.store.setSaved(id, false)
	}

	s.broadcastMessage(fmt.Appendf(nil, `{"type":"save","id":%d,"value":%t}`, id, record.saved))
}

// clear empties the log. Saved requests are the ones you said to keep, so they only go when
// asked to, and then from the archive as well
func (s *Server) clear(from *client, savedToo bool) {
	if s.archive != nil {
		s.archiving.Lock()
		defer s.archiving.Unlock()
	}

	if savedToo && s.archive != nil {
		s.mu.Lock()
		keys := s.store.savedKeys()
		s.mu.Unlock()
		if len(keys) > 0 {
			if err := s.archive.Delete(keys); err != nil {
				fmt.Fprintf(os.Stderr, "Deleting the saved requests from the database failed: %v\n", err)
				from.push(notice("The saved requests are kept: the database still has them"))
				savedToo = false
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.store.clear(savedToo)
	// one message carrying the survivors, so they never blink out of the browser and back
	s.broadcast(event{kind: eventClear, records: s.store.snapshot()})
	s.broadcastMessage(append(appendIDs([]byte(`{"type":"saved","ids":`), s.store.savedIDs()), '}'))
}

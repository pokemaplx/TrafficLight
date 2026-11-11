package web

import (
	"sort"

	"github.com/pokemaplx/TrafficLight/internal/pogo"
)

// Record is one rpc of the log. It only ever holds the bytes the mitm sent: decoding them
// takes about twenty times their size in memory, so that is done when somebody looks at a
// record and thrown away right after.
type Record struct {
	ID   uint64
	Time int64 // unix milliseconds

	RPCID     int64
	RPCStatus int64
	RPCHandle *int64
	Protos    []pogo.RawProto

	// what the record weighs in memory. Guarded by the server's lock, like saved
	size  int64
	saved bool
}

// rough cost of a record and of each of its protos on top of their bytes
const (
	recordOverhead = 160
	protoOverhead  = 64
)

func newRecord(request pogo.Request) *Record {
	record := &Record{
		RPCID:     request.RPCID,
		RPCStatus: request.RPCStatus,
		RPCHandle: request.RPCHandle,
		Protos:    request.Protos,
		size:      recordOverhead,
	}
	for i := range request.Protos {
		record.size += protoOverhead + int64(len(request.Protos[i].Request)+len(request.Protos[i].Response))
	}
	return record
}

// saved records are never dropped, so they need a cap of their own
const saveLimit = 200

// store is the log: the newest records, capped by how many they are and by what they weigh.
// Whichever limit is reached first drops the oldest record that isn't saved.
type store struct {
	// oldest first. Ids only ever grow, so the list is sorted by id as well
	records []*Record
	// records before head are gone. Dropping the oldest only moves head
	head int

	maxRecords int
	// 0: no limit
	maxBytes int64

	bytes      int64
	saved      int
	savedBytes int64
	nextID     uint64
}

func newStore(maxRecords int, maxBytes int64) *store {
	return &store{maxRecords: maxRecords, maxBytes: maxBytes, nextID: 1}
}

func (s *store) len() int { return len(s.records) - s.head }

// snapshot returns the log as it is right now. Records never change, so the copy stays valid
// while the log moves on
func (s *store) snapshot() []*Record {
	return append([]*Record(nil), s.records[s.head:]...)
}

func (s *store) get(id uint64) *Record {
	active := s.records[s.head:]
	i := sort.Search(len(active), func(i int) bool { return active[i].ID >= id })
	if i < len(active) && active[i].ID == id {
		return active[i]
	}
	return nil
}

// add appends a record and returns the ids of what had to go for it, so browsers can agree
func (s *store) add(record *Record) (dropped []uint64) {
	record.ID = s.nextID
	s.nextID++
	s.records = append(s.records, record)
	s.bytes += record.size

	for s.len() > s.maxRecords || (s.maxBytes > 0 && s.bytes > s.maxBytes) {
		// the newest record always stays, even if it alone is over the memory limit
		position := s.oldestUnsaved(len(s.records) - 1)
		if position < 0 {
			break
		}
		dropped = append(dropped, s.records[position].ID)
		s.drop(position)
	}
	s.compact()
	return dropped
}

func (s *store) oldestUnsaved(before int) int {
	for i := s.head; i < before; i++ {
		if !s.records[i].saved {
			return i
		}
	}
	return -1
}

// drop removes the record at position. Only saved records can be in front of it, and there
// are at most saveLimit of those to move
func (s *store) drop(position int) {
	s.bytes -= s.records[position].size
	copy(s.records[s.head+1:position+1], s.records[s.head:position])
	s.records[s.head] = nil
	s.head++
}

// compact gives the space of dropped records back once it is half of the list
func (s *store) compact() {
	if s.head < 256 || s.head < len(s.records)/2 {
		return
	}
	remaining := copy(s.records, s.records[s.head:])
	clear(s.records[remaining:])
	s.records = s.records[:remaining]
	s.head = 0
}

// clear empties the log. Saved records are the ones the user said to keep, so they only go
// when asked to
func (s *store) clear(savedToo bool) {
	var kept []*Record
	if !savedToo {
		for _, record := range s.records[s.head:] {
			if record.saved {
				kept = append(kept, record)
			}
		}
	}

	s.records, s.head = kept, 0
	s.bytes, s.saved, s.savedBytes = 0, 0, 0
	for _, record := range kept {
		s.bytes += record.size
		s.saved++
		s.savedBytes += record.size
	}
}

// maxSaved is never more than half of a small log, or saving would push everything else out
func (s *store) maxSaved() int {
	return min(saveLimit, max(1, s.maxRecords/2))
}

type saveResult uint8

const (
	saveDone saveResult = iota
	saveUnknown
	saveTooMany
	saveTooBig
)

func (s *store) setSaved(id uint64, saved bool) saveResult {
	record := s.get(id)
	if record == nil {
		return saveUnknown
	}
	if record.saved == saved {
		return saveDone
	}

	if !saved {
		// nothing has to be dropped: saved records are part of the limits already
		record.saved = false
		s.saved--
		s.savedBytes -= record.size
		return saveDone
	}

	if s.saved >= s.maxSaved() {
		return saveTooMany
	}
	// half of the memory stays free for the log itself, like half of the records do
	if s.maxBytes > 0 && s.saved > 0 && s.savedBytes+record.size > s.maxBytes/2 {
		return saveTooBig
	}
	record.saved = true
	s.saved++
	s.savedBytes += record.size
	return saveDone
}

func (s *store) savedIDs() []uint64 {
	ids := make([]uint64, 0, s.saved)
	for _, record := range s.records[s.head:] {
		if record.saved {
			ids = append(ids, record.ID)
		}
	}
	return ids
}

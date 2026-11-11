package web

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pokemaplx/TrafficLight/internal/decode"
	"github.com/pokemaplx/TrafficLight/internal/protos"
)

// What browsers fetch over plain http: one proto to inspect, or the log as a file.

func (s *Server) handleProto(w http.ResponseWriter, request *http.Request) {
	id, idErr := strconv.ParseUint(request.PathValue("id"), 10, 64)
	index, indexErr := strconv.Atoi(request.PathValue("index"))

	var record *Record
	if idErr == nil && indexErr == nil {
		s.mu.Lock()
		record = s.store.get(id)
		s.mu.Unlock()
	}
	if record == nil || index < 0 || index >= len(record.Protos) {
		http.Error(w, "This proto isn't available anymore", http.StatusNotFound)
		return
	}

	buffer := getBuffer()
	defer putBuffer(buffer)
	*buffer = decode.AppendDetail((*buffer)[:0], decode.Decode(protos.Default(), record.Protos[index]))

	out, done := compressed(w, request)
	defer done()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = out.Write(*buffer)
}

func (s *Server) handleExport(w http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	records := s.store.snapshot()
	if request.URL.Query().Get("saved") != "" {
		saved := records[:0]
		for _, record := range records {
			if record.saved {
				saved = append(saved, record)
			}
		}
		records = saved
	}
	s.mu.Unlock()
	s.export(w, request, records)
}

// handleExportIDs exports a chosen set of records, i.e. the ones a filter leaves. Too many ids
// for a URL
func (s *Server) handleExportIDs(w http.ResponseWriter, request *http.Request) {
	var body struct {
		IDs *[]json.RawMessage `json:"ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, request.Body, 16<<20)).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if body.IDs == nil {
		http.Error(w, "expected a list of record ids", http.StatusBadRequest)
		return
	}

	wanted := make(map[uint64]struct{}, len(*body.IDs))
	for _, raw := range *body.IDs {
		if id, err := strconv.ParseUint(string(raw), 10, 64); err == nil {
			wanted[id] = struct{}{}
		}
	}

	s.mu.Lock()
	records := s.store.snapshot()
	s.mu.Unlock()
	// walking the log instead of the ids: a repeated id can't make the export any bigger
	chosen := records[:0]
	for _, record := range records {
		if _, ok := wanted[record.ID]; ok {
			chosen = append(chosen, record)
		}
	}
	s.export(w, request, chosen)
}

// export streams records as a json file, one at a time: an export of the whole log is far
// bigger than the log is in memory
func (s *Server) export(w http.ResponseWriter, request *http.Request, records []*Record) {
	filename := time.Now().Format("trafficlight-2006-01-02-150405.json")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	out, done := compressed(w, request)
	defer done()
	buffer := getBuffer()
	defer putBuffer(buffer)

	if _, err := out.Write([]byte{'['}); err != nil {
		return
	}
	for i, record := range records {
		line := (*buffer)[:0]
		if i > 0 {
			line = append(line, ',')
		}
		line = s.appendExport(line, record)
		*buffer = line
		if _, err := out.Write(line); err != nil {
			return
		}
	}
	_, _ = out.Write([]byte{']'})
}

// appendExport appends a record in the same shape mitms send, so an export can be posted to
// Traffic Light again
func (s *Server) appendExport(dst []byte, record *Record) []byte {
	dst = append(dst, `{"time":"`...)
	dst = time.UnixMilli(record.Time).AppendFormat(dst, "2006-01-02T15:04:05.000-07:00")
	dst = append(dst, `","rpcid":`...)
	dst = strconv.AppendInt(dst, record.RPCID, 10)
	dst = append(dst, `,"rpcstatus":`...)
	dst = strconv.AppendInt(dst, record.RPCStatus, 10)
	dst = append(dst, `,"rpchandle":`...)
	if record.RPCHandle != nil {
		dst = strconv.AppendInt(dst, *record.RPCHandle, 10)
	} else {
		dst = append(dst, "null"...)
	}
	dst = append(dst, `,"protos":[`...)
	registry := protos.Default()
	for i := range record.Protos {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = decode.AppendExport(dst, decode.Decode(registry, record.Protos[i]))
	}
	return append(dst, ']', '}')
}

var gzipPool = sync.Pool{New: func() any {
	writer, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return writer
}}

// compressed returns where to write a response to. For browsers that take gzip, which is all
// of them, the json of a proto shrinks to a fraction on its way
func compressed(w http.ResponseWriter, request *http.Request) (io.Writer, func()) {
	if !strings.Contains(request.Header.Get("Accept-Encoding"), "gzip") {
		return w, func() {}
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	writer := gzipPool.Get().(*gzip.Writer)
	writer.Reset(w)
	return writer, func() {
		_ = writer.Close()
		gzipPool.Put(writer)
	}
}

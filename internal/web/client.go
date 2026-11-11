package web

import (
	"context"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// history is sent in chunks of this many records at most
	historyChunkSize = 500
	// and a chunk never holds much more than this much traffic. As text that is five times as
	// much, which is what a browser gets to chew on at once
	chunkBytes = 512 << 10

	// a browser this far behind is hopeless. Dropping it makes it reconnect and start over
	maxClientBacklog      = 1000
	maxClientBacklogBytes = 64 << 20

	writeTimeout = 2 * time.Minute
	pingInterval = 30 * time.Second
	pingTimeout  = 20 * time.Second
)

type eventKind uint8

const (
	// a message that is ready to be sent
	eventMessage eventKind = iota
	// one new record, and what was dropped for it
	eventRecord
	// the log as it was when the browser connected
	eventHistory
	// the log was cleared, records are what survived it
	eventClear
)

// event is something a browser has to be told. Records are not turned into json until they
// are written: the text of a record is several times its size, and a browser that is busy
// loading the history would otherwise sit on a queue full of it.
type event struct {
	kind    eventKind
	message []byte
	record  *Record
	records []*Record
	dropped []uint64
}

// client is a connected browser. Events are queued so they arrive in order without ever
// slowing down the receiver
type client struct {
	conn *websocket.Conn

	mu    sync.Mutex
	queue []event
	// what the records in the queue weigh. They may be gone from the log by now
	queued int64
	wake   chan struct{}
}

func newClient(conn *websocket.Conn) *client {
	return &client{conn: conn, wake: make(chan struct{}, 1)}
}

// push queues an event and tells whether the client keeps up
func (c *client) push(e event) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.queue) > maxClientBacklog || c.queued > maxClientBacklogBytes {
		return false
	}
	c.queue = append(c.queue, e)
	if e.record != nil {
		c.queued += e.record.size
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return true
}

func (c *client) take() []event {
	c.mu.Lock()
	defer c.mu.Unlock()
	events := c.queue
	c.queue, c.queued = nil, 0
	return events
}

// kick closes the connection of a client that is too far behind
func (c *client) kick() {
	if c.conn != nil {
		go c.conn.CloseNow()
	}
}

// write sends what is queued until the connection is gone
func (c *client) write(ctx context.Context, s *Server) error {
	send := func(message []byte) error {
		ctx, cancel := context.WithTimeout(ctx, writeTimeout)
		defer cancel()
		return c.conn.Write(ctx, websocket.MessageText, message)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.wake:
		}
		if err := s.render(c.take(), send); err != nil {
			return err
		}
	}
}

// ping notices connections that died without saying so
func (c *client) ping(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
		err := c.conn.Ping(pingCtx)
		cancel()
		if err != nil {
			c.conn.CloseNow()
			return
		}
	}
}

// render turns events into the messages browsers get, in order. The buffer handed to send is
// only valid until send returns
func (s *Server) render(events []event, send func(message []byte) error) error {
	buffer := getBuffer()
	defer putBuffer(buffer)

	for i := 0; i < len(events); i++ {
		e := events[i]
		switch e.kind {
		case eventMessage:
			if err := send(e.message); err != nil {
				return err
			}

		case eventRecord:
			// records that piled up go out together
			message := append((*buffer)[:0], `{"type":"records","records":[`...)
			dropped := e.dropped
			weight := int64(0)
			for j := i; ; j++ {
				if j > i {
					message = append(message, ',')
					dropped = append(dropped[:len(dropped):len(dropped)], events[j].dropped...)
				}
				message = s.appendRecord(message, events[j].record)
				weight += events[j].record.size
				i = j
				if j+1 >= len(events) || events[j+1].kind != eventRecord || weight >= chunkBytes {
					break
				}
			}
			message = appendDropped(append(message, ']'), dropped)
			*buffer = message
			if err := send(message); err != nil {
				return err
			}

		case eventHistory:
			records := e.records
			for len(records) > 0 {
				count, weight := 0, int64(0)
				for count < len(records) && count < historyChunkSize && weight < chunkBytes {
					weight += records[count].size
					count++
				}
				*buffer = s.appendRecords(append((*buffer)[:0], `{"type":"history","records":[`...), records[:count])
				*buffer = append(*buffer, ']', '}')
				if err := send(*buffer); err != nil {
					return err
				}
				records = records[count:]
			}

		case eventClear:
			// one message carrying the survivors, so they never blink out of the browser and back
			*buffer = s.appendRecords(append((*buffer)[:0], `{"type":"clear","records":[`...), e.records)
			*buffer = append(*buffer, ']', '}')
			if err := send(*buffer); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendDropped(message []byte, dropped []uint64) []byte {
	if len(dropped) > 0 {
		message = append(message, `,"dropped":`...)
		message = appendIDs(message, dropped)
	}
	return append(message, '}')
}

func appendIDs(message []byte, ids []uint64) []byte {
	message = append(message, '[')
	for i, id := range ids {
		if i > 0 {
			message = append(message, ',')
		}
		message = strconv.AppendUint(message, id, 10)
	}
	return append(message, ']')
}

// appendRecords appends the summaries of records, separated by commas. Decoding is what takes
// time here, so every core gets its share of the records
func (s *Server) appendRecords(dst []byte, records []*Record) []byte {
	workers := min(runtime.GOMAXPROCS(0), len(records))
	if workers <= 1 {
		for i, record := range records {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = s.appendRecord(dst, record)
		}
		return dst
	}

	parts := make([]*[]byte, workers)
	var group sync.WaitGroup
	for worker := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			part := getBuffer()
			out := (*part)[:0]
			from, to := len(records)*worker/workers, len(records)*(worker+1)/workers
			for i := from; i < to; i++ {
				if i > 0 {
					out = append(out, ',')
				}
				out = s.appendRecord(out, records[i])
			}
			*part = out
			parts[worker] = part
		}()
	}
	group.Wait()

	for _, part := range parts {
		dst = append(dst, *part...)
		putBuffer(part)
	}
	return dst
}

// appendRecord appends what browsers list of a record. This is where a record's bytes get read
func (s *Server) appendRecord(dst []byte, record *Record) (out []byte) {
	dst = append(dst, `{"id":`...)
	dst = strconv.AppendUint(dst, record.ID, 10)
	dst = append(dst, `,"time":`...)
	dst = strconv.AppendInt(dst, record.Time, 10)
	dst = append(dst, `,"rpc_id":`...)
	dst = strconv.AppendInt(dst, record.RPCID, 10)
	dst = append(dst, `,"rpc_status":`...)
	dst = strconv.AppendInt(dst, record.RPCStatus, 10)
	dst = append(dst, `,"rpc_handle":`...)
	if record.RPCHandle != nil {
		dst = strconv.AppendInt(dst, *record.RPCHandle, 10)
	} else {
		dst = append(dst, "null"...)
	}
	dst = append(dst, `,"protos":[`...)

	// traffic is whatever a phone sent. Should it ever trip up the decoder, the record is
	// listed without its protos instead of taking the connection down
	protos := len(dst)
	defer func() {
		if recover() != nil {
			out = append(dst[:protos], ']', '}')
		}
	}()
	out = dst
	for i := range record.Protos {
		if i > 0 {
			out = append(out, ',')
		}
		out = s.registry.AppendSummary(out, record.Protos[i])
	}
	return append(out, ']', '}')
}

var bufferPool = sync.Pool{New: func() any { return new([]byte) }}

// a buffer that grew for something huge goes to the garbage collector instead of the pool
const maxPooledBuffer = 8 << 20

func getBuffer() *[]byte {
	return bufferPool.Get().(*[]byte)
}

func putBuffer(buffer *[]byte) {
	if cap(*buffer) <= maxPooledBuffer {
		bufferPool.Put(buffer)
	}
}

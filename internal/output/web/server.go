// Package web is the "web" output: the log of requests, and the server browsers look at it
// with.
//
//	server.go   the server, what browsers are told and what they ask for over the websocket
//	store.go    the log: the newest requests, within their limits
//	client.go   a connected browser, and the messages it gets
//	api.go      inspecting a proto and exporting the log
//	archive.go  saved requests that are kept in a database
//	auth.go     the password, and keeping other websites out
//	static.go   the page itself
//	urls.go     where everything is reachable
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/pokemaplx/TrafficLight/internal/archive"
	"github.com/pokemaplx/TrafficLight/internal/traffic"
)

type Options struct {
	// Host is what the web UI listens on. Opening the UI through it is always allowed
	Host string
	// Password of the web UI, empty for none
	Password string
	// MaxRecords and MaxBytes cap the log, see store. MaxBytes 0 means no limit
	MaxRecords int
	MaxBytes   int64
	// ReceiverURL is shown to the user: where their mitm has to send its traffic
	ReceiverURL string
	// Archive keeps saved requests past a restart. Without one they live in the log only
	Archive archive.Store
}

type Server struct {
	options Options
	auth    *auth
	handler http.Handler
	static  map[string]staticFile
	// lets browsers tell a reconnect from a restart
	session string

	archive archive.Store
	// held while a saved request is written to or deleted from the archive
	archiving sync.Mutex

	// guards everything below. Nothing slow ever happens while it is held: records are only
	// decoded by whoever writes them to a browser
	mu      sync.Mutex
	store   *store
	clients map[*client]struct{}
	paused  bool
}

func New(options Options) *Server {
	session := make([]byte, 8)
	_, _ = rand.Read(session)

	s := &Server{
		options: options,
		auth:    newAuth(options.Password),
		archive: options.Archive,
		session: hex.EncodeToString(session),
		store:   newStore(options.MaxRecords, options.MaxBytes),
		clients: map[*client]struct{}{},
	}
	s.static = loadStatic()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.serveStatic("index.html"))
	// browsers ask for it on their own, i.e. for bookmarks
	mux.HandleFunc("GET /favicon.ico", s.serveStatic("favicon.ico"))
	mux.HandleFunc("GET /static/{name}", s.handleStatic(""))
	mux.HandleFunc("GET /static/js/{name}", s.handleStatic("js/"))
	mux.HandleFunc("GET /static/fonts/{name}", s.handleStatic("fonts/"))
	mux.HandleFunc("GET /ws", s.handleWebsocket)
	mux.HandleFunc("GET /api/records/{id}/{index}", s.handleProto)
	mux.HandleFunc("GET /api/export", s.handleExport)
	mux.HandleFunc("POST /api/export", s.handleExportIDs)
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, _ *http.Request) {
		// only reachable with a valid session, see loginRequired
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)
	s.handler = s.guard(s.loginRequired(mux))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	s.handler.ServeHTTP(w, request)
}

// AuthEnabled tells whether the web UI asks for a password
func (s *Server) AuthEnabled() bool { return s.auth.enabled }

// Add puts an rpc into the log and tells every browser about it
func (s *Server) Add(request traffic.Request) {
	record := newRecord(request)
	record.Time = time.Now().UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paused {
		return
	}
	dropped := s.store.add(record)
	if len(s.clients) > 0 {
		s.broadcast(event{kind: eventRecord, record: record, dropped: dropped})
	}
}

// broadcast needs the lock to be held
func (s *Server) broadcast(e event) {
	for client := range s.clients {
		if !client.push(e) {
			delete(s.clients, client)
			client.kick()
		}
	}
}

func (s *Server) broadcastMessage(message []byte) {
	s.broadcast(event{kind: eventMessage, message: message})
}

// what browsers send
type browserCommand struct {
	Type  string          `json:"type"`
	ID    json.RawMessage `json:"id"`
	Value json.RawMessage `json:"value"`
	Saved json.RawMessage `json:"saved"`
}

func (s *Server) handleCommand(from *client, data []byte) {
	var command browserCommand
	if json.Unmarshal(data, &command) != nil {
		return
	}

	switch command.Type {
	case "pause":
		s.mu.Lock()
		defer s.mu.Unlock()
		s.paused = truthy(command.Value)
		s.broadcastMessage(fmt.Appendf(nil, `{"type":"state","paused":%t}`, s.paused))

	case "save":
		if id, err := strconv.ParseUint(string(command.ID), 10, 64); err == nil {
			s.setSaved(from, id, truthy(command.Value))
		}

	case "clear":
		s.clear(from, truthy(command.Saved))
	}
}

func notice(text string) event {
	message, _ := json.Marshal(map[string]string{"type": "notice", "text": text})
	return event{kind: eventMessage, message: message}
}

// truthy reads a json value the way a script would: false, null, 0, "" and the empty list or
// object are not
func truthy(value json.RawMessage) bool {
	switch strings.TrimSpace(string(value)) {
	case "", "false", "null", "0", `""`, "[]", "{}", "0.0", "-0":
		return false
	}
	return true
}

func (s *Server) hello() []byte {
	message, _ := json.Marshal(map[string]any{
		"type":        "hello",
		"session":     s.session,
		"paused":      s.paused,
		"max_records": s.options.MaxRecords,
		"receiver":    s.options.ReceiverURL,
		"auth":        s.auth.enabled,
		"saved":       s.store.savedIDs(),
	})
	return message
}

func (s *Server) handleWebsocket(w http.ResponseWriter, request *http.Request) {
	conn, err := websocket.Accept(w, request, &websocket.AcceptOptions{
		// the text of a record compresses to a fraction of its size. Without context takeover
		// a connection holds no compression state between messages
		CompressionMode: websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	// commands are a few bytes
	conn.SetReadLimit(64 << 10)

	// the hello and the log are queued in the same breath the client is registered in, so it
	// can't miss or double a record
	client := newClient(conn)
	s.mu.Lock()
	client.push(event{kind: eventMessage, message: s.hello()})
	client.push(event{kind: eventHistory, records: s.store.snapshot()})
	s.clients[client] = struct{}{}
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(request.Context())
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.clients, client)
		s.mu.Unlock()
	}()

	go func() {
		if client.write(ctx, s) != nil {
			conn.CloseNow()
		}
	}()
	go client.ping(ctx)

	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if kind == websocket.MessageText {
			s.handleCommand(client, data)
		}
	}
}

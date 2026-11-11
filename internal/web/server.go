// Package web is the "web" output: the log of requests, and the server browsers look at it with
package web

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/pokemaplx/TrafficLight/internal/pogo"
)

//go:embed static
var staticFiles embed.FS

// the pattern has no dot or slash in it, so it can't walk out of the directory
var scriptName = regexp.MustCompile(`^[a-z]+\.js$`)

const noCache = "no-cache"

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
	Archive Archive
}

type Server struct {
	options  Options
	auth     *auth
	registry *pogo.Registry
	handler  http.Handler
	static   map[string]staticFile
	// lets browsers tell a reconnect from a restart
	session string

	archive Archive
	// held while a saved request is written to or deleted from the archive
	archiving sync.Mutex

	// guards everything below. Nothing slow ever happens while it is held: records are only
	// decoded by whoever writes them to a browser
	mu      sync.Mutex
	store   *store
	clients map[*client]struct{}
	paused  bool
}

type staticFile struct {
	path         string
	contentType  string
	cacheControl string
	// browsers ask whether their copy is still good, and the tag lets the answer be a short yes
	etag string
}

func New(options Options) *Server {
	session := make([]byte, 8)
	_, _ = rand.Read(session)

	s := &Server{
		options:  options,
		auth:     newAuth(options.Password),
		registry: pogo.Default(),
		archive:  options.Archive,
		session:  hex.EncodeToString(session),
		store:    newStore(options.MaxRecords, options.MaxBytes),
		clients:  map[*client]struct{}{},
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
func (s *Server) Add(request pogo.Request) {
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
	*buffer = pogo.AppendDetail((*buffer)[:0], s.registry.Decode(record.Protos[index]))

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
	for i := range record.Protos {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = pogo.AppendExport(dst, s.registry.Decode(record.Protos[i]))
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

func (s *Server) handleLoginPage(w http.ResponseWriter, request *http.Request) {
	if !s.auth.enabled || s.auth.loggedIn(request) {
		http.Redirect(w, request, "/", http.StatusFound)
		return
	}
	s.serveStatic("login.html")(w, request)
}

func (s *Server) handleLogin(w http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(w, request.Body, 64<<10)
	if s.auth.enabled && !s.auth.checkPassword(request.PostFormValue("password")) {
		w.Header().Set("Location", "/login?error")
		w.WriteHeader(http.StatusSeeOther)
		return
	}

	if s.auth.enabled {
		setSessionCookie(w, request, s.auth.newSession(), sessionLifetime)
	}
	w.Header().Set("Location", "/")
	w.WriteHeader(http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, request *http.Request) {
	setSessionCookie(w, request, "", 0)
	w.Header().Set("Location", "/login")
	w.WriteHeader(http.StatusSeeOther)
}

// guard keeps other websites away from your traffic. Checking Origin prevents cross-site
// websockets. Without a password, checking Host prevents DNS rebinding. With one, the session
// cookie does: it never goes to another domain.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !s.auth.enabled && !s.trustedHost(hostname(request.Host)) {
			http.Error(w, "Open Traffic Light through localhost or an IP address, or set a password to use a domain", http.StatusForbidden)
			return
		}

		if origin := request.Header.Get("Origin"); origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(parsed.Host, request.Host) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, request)
	})
}

func (s *Server) loginRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		// the static files are in the public repository anyway, the login page needs them
		public := path == "/login" || path == "/favicon.ico" || strings.HasPrefix(path, "/static/")
		if !s.auth.enabled || public || s.auth.loggedIn(request) {
			next.ServeHTTP(w, request)
			return
		}

		if path == "/" {
			http.Redirect(w, request, "/login", http.StatusFound)
			return
		}
		http.Error(w, "Log in first", http.StatusUnauthorized)
	})
}

func hostname(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

func (s *Server) trustedHost(host string) bool {
	if host == "" {
		return false
	}
	if host == "localhost" || host == strings.ToLower(s.options.Host) {
		return true
	}
	// ip addresses can't be rebound, only domains can
	return net.ParseIP(host) != nil
}

// loadStatic lists what the web UI is made of: the page, its handful of ES modules and fonts
func loadStatic() map[string]staticFile {
	files := map[string]staticFile{}
	add := func(name, contentType, cacheControl string) {
		content, err := staticFiles.ReadFile("static/" + name)
		if err != nil {
			panic("the web UI is missing " + name)
		}
		sum := sha256.Sum256(content)
		files[name] = staticFile{
			path:         "static/" + name,
			contentType:  contentType,
			cacheControl: cacheControl,
			etag:         `"` + hex.EncodeToString(sum[:12]) + `"`,
		}
	}

	add("index.html", "text/html; charset=utf-8", noCache)
	add("login.html", "text/html; charset=utf-8", noCache)
	add("style.css", "text/css; charset=utf-8", noCache)
	add("logo.png", "image/png", noCache)
	add("favicon.ico", "image/x-icon", noCache)

	scripts, _ := fs.ReadDir(staticFiles, "static/js")
	for _, script := range scripts {
		if scriptName.MatchString(script.Name()) {
			add("js/"+script.Name(), "text/javascript; charset=utf-8", noCache)
		}
	}
	// Inter and JetBrains Mono, licensed under the SIL Open Font License (see static/fonts).
	// Fonts never change, unlike the rest of the page
	for _, font := range []string{"inter-latin", "inter-latin-ext", "jetbrains-mono-latin", "jetbrains-mono-latin-ext"} {
		add("fonts/"+font+".woff2", "font/woff2", "max-age=604800")
	}
	return files
}

func (s *Server) handleStatic(directory string) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		name := directory + request.PathValue("name")
		// the login page is only served by /login, and index.html by /
		if name == "login.html" {
			http.NotFound(w, request)
			return
		}
		s.serveStatic(name)(w, request)
	}
}

func (s *Server) serveStatic(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		file, ok := s.static[name]
		if !ok {
			http.NotFound(w, request)
			return
		}
		content, err := staticFiles.Open(file.path)
		if err != nil {
			http.NotFound(w, request)
			return
		}
		defer content.Close()

		w.Header().Set("Content-Type", file.contentType)
		w.Header().Set("Cache-Control", file.cacheControl)
		w.Header().Set("ETag", file.etag)
		// straight out of the binary, nothing is copied into memory
		http.ServeContent(w, request, "", time.Time{}, content.(io.ReadSeeker))
	}
}

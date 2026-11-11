package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/pokemaplx/TrafficLight/internal/receiver"
)

// What a browser and a mitm see of Traffic Light, over real connections.

func serve(t *testing.T, options Options) (*Server, *httptest.Server) {
	t.Helper()
	if options.MaxRecords == 0 {
		options.MaxRecords = 100
	}
	if options.Host == "" {
		options.Host = "127.0.0.1"
	}
	s := New(options)
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	return s, server
}

// noRedirect shows redirects instead of following them
func noRedirect() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func get(t *testing.T, client *http.Client, url string, headers ...string) (*http.Response, string) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, url, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i] == "Host" {
			request.Host = headers[i+1]
		} else {
			request.Header.Set(headers[i], headers[i+1])
		}
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response, string(body)
}

func dial(t *testing.T, server *httptest.Server, header http.Header) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", &websocket.DialOptions{
		HTTPHeader:      header,
		CompressionMode: websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(-1)
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func read(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("not json: %s", data)
	}
	return message
}

func TestTheUIIsServed(t *testing.T) {
	_, server := serve(t, Options{})
	client := noRedirect()

	for path, contentType := range map[string]string{
		"/":                               "text/html; charset=utf-8",
		"/favicon.ico":                    "image/x-icon",
		"/static/style.css":               "text/css; charset=utf-8",
		"/static/logo.png":                "image/png",
		"/static/js/main.js":              "text/javascript; charset=utf-8",
		"/static/fonts/inter-latin.woff2": "font/woff2",
	} {
		response, body := get(t, client, server.URL+path)
		if response.StatusCode != http.StatusOK || len(body) == 0 {
			t.Errorf("%s: status %d, %d bytes", path, response.StatusCode, len(body))
		}
		expect(t, path+" content type", response.Header.Get("Content-Type"), contentType)

		// a browser that has it already only gets told so
		again, _ := get(t, client, server.URL+path, "If-None-Match", response.Header.Get("ETag"))
		expect(t, path+" revalidated", again.StatusCode, http.StatusNotModified)
	}

	for _, path := range []string{"/static/jsconfig.json", "/static/js/../index.html", "/static/login.html", "/static/fonts/OFL-Inter.txt", "/nothing"} {
		if response, _ := get(t, client, server.URL+path); response.StatusCode == http.StatusOK {
			t.Errorf("%s is served", path)
		}
	}
}

func TestOtherWebsitesAreKeptOut(t *testing.T) {
	_, server := serve(t, Options{})
	client := noRedirect()

	response, _ := get(t, client, server.URL+"/", "Host", "evil.example")
	expect(t, "a domain without a password", response.StatusCode, http.StatusForbidden)
	response, _ = get(t, client, server.URL+"/", "Host", "localhost:3336")
	expect(t, "localhost", response.StatusCode, http.StatusOK)
	response, _ = get(t, client, server.URL+"/api/session", "Origin", "http://evil.example")
	expect(t, "another website's script", response.StatusCode, http.StatusForbidden)

	// with a password a domain is fine: the session cookie never goes to another one
	_, protected := serve(t, Options{Password: "hunter2"})
	response, _ = get(t, client, protected.URL+"/login", "Host", "traffic.example")
	expect(t, "a domain with a password", response.StatusCode, http.StatusOK)
}

func TestThePasswordIsAskedFor(t *testing.T) {
	_, server := serve(t, Options{Password: "hunter2"})
	client := noRedirect()

	response, _ := get(t, client, server.URL+"/")
	expect(t, "the page sends you to the login", response.StatusCode, http.StatusFound)
	expect(t, "location", response.Header.Get("Location"), "/login")
	response, _ = get(t, client, server.URL+"/api/session")
	expect(t, "the api refuses", response.StatusCode, http.StatusUnauthorized)
	response, _ = get(t, client, server.URL+"/ws")
	expect(t, "the websocket refuses", response.StatusCode, http.StatusUnauthorized)
	response, _ = get(t, client, server.URL+"/static/style.css")
	expect(t, "the login page gets its styles", response.StatusCode, http.StatusOK)

	post, err := client.PostForm(server.URL+"/login", map[string][]string{"password": {"hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	post.Body.Close()
	expect(t, "the right password logs in", post.Header.Get("Location"), "/")
	response, _ = get(t, client, server.URL+"/api/session")
	expect(t, "and the session is valid", response.StatusCode, http.StatusNoContent)

	logout, err := client.Post(server.URL+"/logout", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	logout.Body.Close()
	response, _ = get(t, client, server.URL+"/api/session")
	expect(t, "logging out ends it", response.StatusCode, http.StatusUnauthorized)
}

func TestSessionsAreSignedAndExpire(t *testing.T) {
	a := newAuth("hunter2")
	session := a.newSession()
	if !a.validSession(session) {
		t.Error("a fresh session isn't valid")
	}
	if a.validSession(session+"0") || a.validSession("9999999999."+strings.Repeat("0", 64)) || a.validSession("") {
		t.Error("a forged session is taken")
	}
	if a.validSession("1." + a.sign(1)) {
		t.Error("an expired session is taken")
	}
	if newAuth("another").validSession(session) {
		t.Error("changing the password doesn't end the sessions")
	}
}

func TestABrowserFollowsTheLog(t *testing.T) {
	s, server := serve(t, Options{ReceiverURL: "http://receiver:3335"})
	s.Add(request(10))
	s.Add(request(10))

	conn := dial(t, server, nil)
	hello := read(t, conn)
	expect(t, "it is greeted", hello["type"], "hello")
	expect(t, "and told where traffic goes", hello["receiver"], "http://receiver:3335")
	history := read(t, conn)
	expect(t, "then gets the log", history["type"], "history")
	expect(t, "all of it", recordIDs(history), []uint64{1, 2})

	s.Add(request(10))
	live := read(t, conn)
	expect(t, "new records follow", live["type"], "records")
	expect(t, "as they come", recordIDs(live), []uint64{3})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"save","id":2,"value":true}`)); err != nil {
		t.Fatal(err)
	}
	expect(t, "commands are answered", read(t, conn), map[string]any{"type": "save", "id": float64(2), "value": true})

	conn.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		clients := len(s.clients)
		s.mu.Unlock()
		if clients == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a browser that left is still kept")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAProtoCanBeInspectedAndExported(t *testing.T) {
	s, server := serve(t, Options{})
	client := noRedirect()

	// a GetMapObjects request with a few cells, the way a mitm posts it
	traffic := &receiver.Receiver{Sink: s.Add}
	payload := base64.StdEncoding.EncodeToString([]byte{0x0a, 0x03, 0x01, 0x02, 0x03, 0x19, 0, 0, 0, 0, 0, 0x40, 0x43, 0x40})
	body := `{"rpcid":5,"rpcstatus":1,"rpchandle":9,"contents":[{"type":106,"request":"` + payload + `","payload":""}]}`
	recorder := httptest.NewRecorder()
	traffic.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	expect(t, "the receiver takes it", recorder.Body.String(), "OK")

	response, detail := get(t, client, server.URL+"/api/records/1/0")
	expect(t, "the proto is there", response.StatusCode, http.StatusOK)
	var parsed struct {
		Method  string
		Request struct {
			Name   string
			State  string
			Base64 string
			Text   string
			JSON   map[string]any
			Tree   map[string]any
		}
	}
	if err := json.Unmarshal([]byte(detail), &parsed); err != nil {
		t.Fatalf("the detail isn't json: %v\n%s", err, detail)
	}
	expect(t, "method", parsed.Method, "METHOD_GET_MAP_OBJECTS")
	expect(t, "message", parsed.Request.Name, "GetMapObjectsProto")
	expect(t, "state", parsed.Request.State, "ok")
	expect(t, "bytes", parsed.Request.Base64, payload)
	expect(t, "text", parsed.Request.Text, "cell_id: 1\ncell_id: 2\ncell_id: 3\nplayer_lat: 38.5\n")
	expect(t, "json", parsed.Request.JSON, map[string]any{"cell_id": []any{"1", "2", "3"}, "player_lat": 38.5})
	expect(t, "tree", parsed.Request.Tree["t"], "GetMapObjectsProto")

	for _, path := range []string{"/api/records/1/1", "/api/records/2/0", "/api/records/x/0", "/api/records/1/-1"} {
		response, _ = get(t, client, server.URL+path)
		expect(t, path, response.StatusCode, http.StatusNotFound)
	}

	// an export has the shape mitms send, so it can be posted right back
	response, exported := get(t, client, server.URL+"/api/export")
	if !strings.Contains(response.Header.Get("Content-Disposition"), "trafficlight-") {
		t.Errorf("the export isn't a download: %q", response.Header.Get("Content-Disposition"))
	}
	recorder = httptest.NewRecorder()
	traffic.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(exported)))
	expect(t, "the export is taken back", recorder.Body.String(), "OK")
	first, second := s.store.get(1), s.store.get(2)
	if second == nil || !bytes.Equal(first.Protos[0].Request, second.Protos[0].Request) || second.RPCID != 5 || *second.RPCHandle != 9 {
		t.Errorf("the export doesn't come back as what it was: %+v", second)
	}

	chosen, err := client.Post(server.URL+"/api/export", "application/json", strings.NewReader(`{"ids":[2,2,77,"x"]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer chosen.Body.Close()
	var records []map[string]any
	if err := json.NewDecoder(chosen.Body).Decode(&records); err != nil {
		t.Fatal(err)
	}
	expect(t, "a chosen set is exported once each", len(records), 1)

	response, exported = get(t, client, server.URL+"/api/export?saved=1")
	expect(t, "nothing is saved", strings.TrimSpace(exported), "[]")
}

package forward

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Forwarding sits in the receiver's path, so what matters here is that it passes bodies on
// untouched and that a bad target stays harmless.

// target stands in for whatever you forward to, i.e. a Golbat
type target struct {
	*httptest.Server
	mu      sync.Mutex
	status  int
	bodies  []string
	headers []http.Header
}

func newTarget(t *testing.T) *target {
	target := &target{status: http.StatusOK}
	target.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		target.mu.Lock()
		target.bodies = append(target.bodies, string(body))
		target.headers = append(target.headers, r.Header.Clone())
		status := target.status
		target.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(target.Close)
	return target
}

func quiet(f *Forwarder) *Forwarder {
	f.Logf = func(string, ...any) {}
	return f
}

func TestABodyIsForwardedUntouched(t *testing.T) {
	// golbat's own shape, to make sure nothing re-serializes it into Traffic Light's
	body := `{"username":"a","contents":[{"type":106,"request":"","payload":""}]}`
	target := newTarget(t)
	forwarder := quiet(New(target.URL+"/raw", ""))
	forwarder.Send([]byte(body))
	forwarder.wait()
	forwarder.Close()

	if len(target.bodies) != 1 || target.bodies[0] != body {
		t.Fatalf("the body didn't arrive byte for byte: %q", target.bodies)
	}
	if got := target.headers[0].Get("Content-Type"); got != "application/json" {
		t.Errorf("it isn't sent as json: %q", got)
	}
	if got := target.headers[0].Get("Authorization"); got != "" {
		t.Errorf("a token is sent without one being set: %q", got)
	}
}

func TestATokenIsSentAsABearerToken(t *testing.T) {
	target := newTarget(t)
	forwarder := quiet(New(target.URL, "s3cret"))
	forwarder.Send([]byte("{}"))
	forwarder.wait()
	forwarder.Close()

	if got := target.headers[0].Get("Authorization"); got != "Bearer s3cret" {
		t.Errorf("the token isn't a bearer token: %q", got)
	}
}

func TestNothingIsForwardedWithoutAURL(t *testing.T) {
	forwarder := quiet(New("", ""))
	if forwarder.Enabled() {
		t.Error("an empty url is enabled")
	}
	forwarder.Send([]byte("{}"))
	if forwarder.inFlight != 0 {
		t.Error("sending did something")
	}
}

func TestAnUnreachableTargetIsHarmless(t *testing.T) {
	// port 1 is never anything, so this can only fail to connect
	forwarder := quiet(New("http://127.0.0.1:1/raw", ""))
	forwarder.Send([]byte("{}"))
	forwarder.wait()
	if !forwarder.failing {
		t.Error("a failure isn't noticed")
	}
}

func TestAnErrorStatusIsReportedOnce(t *testing.T) {
	target := newTarget(t)
	target.status = http.StatusInternalServerError

	var logged []string
	forwarder := New(target.URL, "")
	forwarder.Logf = func(format string, _ ...any) { logged = append(logged, format) }

	for i := 0; i < 3; i++ {
		forwarder.Send([]byte("{}"))
		forwarder.wait()
	}
	if !forwarder.failing {
		t.Error("an error status doesn't count as failing")
	}
	if len(logged) != 1 {
		t.Errorf("a dead target is logged %d times instead of once", len(logged))
	}

	// recovering says so again, so the log tells you the truth either way
	target.mu.Lock()
	target.status = http.StatusOK
	target.mu.Unlock()
	forwarder.Send([]byte("{}"))
	forwarder.wait()
	if forwarder.failing {
		t.Error("recovering doesn't clear the failure")
	}
	if len(logged) != 2 {
		t.Errorf("recovering isn't logged: %d lines", len(logged))
	}
}

func TestRequestsAreDroppedInsteadOfPilingUp(t *testing.T) {
	held := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-held }))
	defer server.Close()

	forwarder := quiet(New(server.URL, ""))
	for i := 0; i < MaxInFlight+10; i++ {
		forwarder.Send([]byte("{}"))
	}

	forwarder.mu.Lock()
	inFlight, dropped := forwarder.inFlight, forwarder.dropped
	forwarder.mu.Unlock()
	if inFlight != MaxInFlight {
		t.Errorf("in flight requests aren't capped: %d", inFlight)
	}
	if dropped != 10 {
		t.Errorf("the overflow isn't counted as dropped: %d", dropped)
	}

	close(held)
	forwarder.wait()
	forwarder.Close()
}

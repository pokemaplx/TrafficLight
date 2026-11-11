// Package forward passes every request the receiver gets on to another endpoint.
//
// That way Traffic Light can sit in front of a scanner instead of taking its place: point your
// mitm at Traffic Light and Traffic Light at the scanner's own endpoint, i.e. a Golbat's /raw.
//
// Forwarding is fire and forget. The receiver answers the mitm without ever waiting on the
// other end, nothing is retried, and a target that is down or slow only costs the forwarded
// requests themselves.
package forward

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// a target that can't keep up would otherwise pile up requests until it runs us out of memory
const MaxInFlight = 100

const timeout = 10 * time.Second

type Forwarder struct {
	url    string
	token  string
	client *http.Client
	// Logf prints what changed. A dead target would print on every single request, so only
	// changes are worth saying
	Logf func(format string, args ...any)

	mu       sync.Mutex
	inFlight int
	failing  bool
	dropped  int
	closed   bool
	done     sync.WaitGroup
}

func New(url string, token string) *Forwarder {
	return &Forwarder{
		url:   url,
		token: token,
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				MaxIdleConnsPerHost: 16,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		Logf: func(format string, args ...any) { fmt.Printf(format+"\n", args...) },
	}
}

func (f *Forwarder) Enabled() bool {
	return f.url != ""
}

// Send hands a body off to be forwarded. It never blocks the receiver
func (f *Forwarder) Send(body []byte) {
	if !f.Enabled() {
		return
	}

	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	if f.inFlight >= MaxInFlight {
		f.dropped++
		f.markFailing("it can't keep up")
		f.mu.Unlock()
		return
	}
	f.inFlight++
	f.done.Add(1)
	f.mu.Unlock()

	go f.post(body)
}

func (f *Forwarder) post(body []byte) {
	reason := f.deliver(body)

	f.mu.Lock()
	f.inFlight--
	if reason != "" {
		f.markFailing(reason)
	} else {
		f.markWorking()
	}
	f.mu.Unlock()
	// last, so whoever waits for this finds everything settled
	f.done.Done()
}

// deliver returns why it didn't work, or nothing if it did
func (f *Forwarder) deliver(body []byte) string {
	request, err := http.NewRequest(http.MethodPost, f.url, bytes.NewReader(body))
	if err != nil {
		return err.Error()
	}
	request.Header.Set("Content-Type", "application/json")
	if f.token != "" {
		request.Header.Set("Authorization", "Bearer "+f.token)
	}

	response, err := f.client.Do(request)
	if err != nil {
		return err.Error()
	}
	// reading the answer to its end lets the connection be used again
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	response.Body.Close()

	if response.StatusCode >= 400 {
		return fmt.Sprintf("it answered %d", response.StatusCode)
	}
	return ""
}

func (f *Forwarder) markFailing(reason string) {
	if f.failing || f.closed {
		return
	}
	f.failing = true
	f.Logf("Forwarding to %s failed: %s. Nothing is retried, Traffic Light keeps running", f.url, reason)
}

func (f *Forwarder) markWorking() {
	if !f.failing || f.closed {
		return
	}
	f.failing = false
	missed := ""
	if f.dropped > 0 {
		missed = fmt.Sprintf(", %d request(s) were dropped in between", f.dropped)
	}
	f.dropped = 0
	f.Logf("Forwarding to %s works again%s", f.url, missed)
}

// Close stops forwarding. What is in flight is abandoned
func (f *Forwarder) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.client.CloseIdleConnections()
}

// wait blocks until nothing is in flight, which a receiver never does
func (f *Forwarder) wait() {
	f.done.Wait()
}

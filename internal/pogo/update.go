package pogo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

// UpstreamProtos is where new protos show up: the original Traffic Light gets a pogo_pb2.py
// with every version of the game
const UpstreamProtos = "https://raw.githubusercontent.com/ccev/TrafficLight/main/trafficlight/protos/pogo_pb2.py"

// nothing that describes protos is anywhere near this big
const maxSourceSize = 64 << 20

// Updater keeps the protos current while Traffic Light runs. It looks at where they come
// from, and when something new is there it builds a registry from it and puts it in use.
// What it can't read or what doesn't hold up is left alone: the protos in use stay.
type Updater struct {
	// Protos is where the protos' descriptor comes from, in any form DescriptorFrom takes.
	// Methods is an optional methods file (see LoadWith). Both are an http(s) url or a path
	// to a file, and empty for none
	Protos  string
	Methods string
	// Logf says what changed, and once what went wrong
	Logf func(format string, args ...any)

	client     *http.Client
	protos     source
	methods    source
	descriptor []byte
	extra      []Method
	failing    bool
	checked    bool
}

// source remembers what was last read from somewhere, to tell whether it changed
type source struct {
	etag string
	sum  [sha256.Size]byte
	read bool
}

// Check looks for new protos once and puts them in use. It tells whether anything changed
func (u *Updater) Check(ctx context.Context) bool {
	if u.client == nil {
		u.client = &http.Client{Timeout: time.Minute}
	}
	if u.descriptor == nil {
		u.descriptor = embedded
	}

	changed, err := u.check(ctx)
	switch {
	case err != nil && !u.failing:
		// the same thing every few hours isn't worth repeating
		u.logf("Looking for new protos failed: %v. The ones in use stay", err)
	case err == nil && u.failing:
		u.logf("Looking for new protos works again")
	}
	u.failing = err != nil
	return changed
}

func (u *Updater) check(ctx context.Context) (bool, error) {
	descriptor, extra := u.descriptor, u.extra
	changed, newProtos, newMethods := false, false, false
	var failure error

	if u.Protos != "" {
		data, fresh, err := u.protos.fetch(ctx, u.client, u.Protos)
		if err == nil && fresh {
			// often these are the protos Traffic Light was built with
			if descriptor, err = DescriptorFrom(data); err == nil && !bytes.Equal(descriptor, u.descriptor) {
				changed, newProtos = true, true
			}
		}
		if err != nil {
			failure = fmt.Errorf("%s: %w", u.Protos, err)
			descriptor = u.descriptor
		}
	}
	if u.Methods != "" {
		data, fresh, err := u.methods.fetch(ctx, u.client, u.Methods)
		if err == nil && fresh {
			if extra, err = MethodsFrom(data); err == nil {
				changed, newMethods = true, true
			}
		}
		if err != nil {
			failure = errors.Join(failure, fmt.Errorf("%s: %w", u.Methods, err))
			extra = u.extra
		}
	}
	if !changed {
		if failure == nil && !u.checked {
			u.logf("The protos in use are the newest there are")
		}
		u.checked = u.checked || failure == nil
		return false, failure
	}
	u.checked = true

	// the same checks the built in protos went through
	registry, err := LoadWith(descriptor, extra)
	if err != nil {
		return false, errors.Join(failure, fmt.Errorf("the new protos don't hold up: %w", err))
	}

	before := Default()
	u.descriptor, u.extra = descriptor, extra
	SetDefault(registry)
	if newProtos {
		u.logf("Protos updated: %d messages and %d methods, there were %d and %d",
			registry.Messages(), registry.Methods()-registry.Named(), before.Messages(), before.Methods()-before.Named())
	}
	if newMethods || registry.Named() != before.Named() || registry.Mapped() != before.Mapped() {
		u.logf("%s names %d methods the protos don't know yet, and tells the messages of %d more",
			u.Methods, registry.Named(), registry.Mapped())
	}
	// building a registry leaves a lot behind, and the old one is garbage now
	debug.FreeOSMemory()
	return true, failure
}

// Run checks right away and then every so often, until the context ends. With every being
// zero it checks once
func (u *Updater) Run(ctx context.Context, every time.Duration) {
	u.Check(ctx)
	if every <= 0 {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.Check(ctx)
		}
	}
}

func (u *Updater) logf(format string, args ...any) {
	if u.Logf != nil {
		u.Logf(format, args...)
	}
}

// fetch reads a source and tells whether it holds something else than the last time
func (s *source) fetch(ctx context.Context, client *http.Client, location string) (data []byte, fresh bool, err error) {
	if strings.HasPrefix(location, "http://") || strings.HasPrefix(location, "https://") {
		data, err = s.download(ctx, client, location)
	} else {
		data, err = os.ReadFile(location)
		if err == nil && len(data) > maxSourceSize {
			err = errors.New("it is too big to be protos")
		}
	}
	if err != nil || data == nil {
		return nil, false, err
	}

	sum := sha256.Sum256(data)
	if s.read && sum == s.sum {
		return nil, false, nil
	}
	s.sum, s.read = sum, true
	return data, true, nil
}

// download returns nothing if the server says it is still what it was
func (s *source) download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "TrafficLight")
	if s.etag != "" {
		request.Header.Set("If-None-Match", s.etag)
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNotModified:
		return nil, nil
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("the server answered %d", response.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxSourceSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSourceSize {
		return nil, errors.New("it is too big to be protos")
	}
	s.etag = response.Header.Get("ETag")
	return data, nil
}

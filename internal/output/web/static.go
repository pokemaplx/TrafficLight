package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"time"

	"github.com/pokemaplx/TrafficLight/ui"
)

// The page itself: what package ui holds, served under /static.

// the pattern has no dot or slash in it, so it can't walk out of the directory
var scriptName = regexp.MustCompile(`^[a-z]+\.js$`)

const noCache = "no-cache"

type staticFile struct {
	path         string
	contentType  string
	cacheControl string
	// browsers ask whether their copy is still good, and the tag lets the answer be a short yes
	etag string
}

// loadStatic lists what the web UI is made of: the page, its handful of ES modules and fonts
func loadStatic() map[string]staticFile {
	files := map[string]staticFile{}
	add := func(name, contentType, cacheControl string) {
		content, err := ui.Files.ReadFile(name)
		if err != nil {
			panic("the web UI is missing " + name)
		}
		sum := sha256.Sum256(content)
		files[name] = staticFile{
			path:         name,
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

	scripts, _ := fs.ReadDir(ui.Files, "js")
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
		content, err := ui.Files.Open(file.path)
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

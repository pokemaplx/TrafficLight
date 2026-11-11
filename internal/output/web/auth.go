package web

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	cookieName       = "trafficlight_session"
	sessionLifetime  = 30 * 24 * time.Hour
	failedLoginDelay = time.Second
)

// auth is the optional password of the web UI. Sessions are signed cookies, so they survive
// restarts and changing the password ends all of them.
type auth struct {
	enabled  bool
	password []byte
	key      []byte
	// wrong passwords wait in line, so trying many at once is no faster than one after another
	failures sync.Mutex
}

func newAuth(password string) *auth {
	a := &auth{enabled: password != "", password: []byte(password)}
	if a.enabled {
		// slow on purpose, so a leaked cookie doesn't make guessing the password cheap
		a.key, _ = pbkdf2.Key(sha256.New, password, []byte("trafficlight-session"), 200_000, sha256.Size)
	}
	return a
}

func (a *auth) checkPassword(password string) bool {
	if subtle.ConstantTimeCompare([]byte(password), a.password) == 1 {
		return true
	}

	a.failures.Lock()
	time.Sleep(failedLoginDelay)
	a.failures.Unlock()
	return false
}

func (a *auth) newSession() string {
	expires := time.Now().Add(sessionLifetime).Unix()
	return strconv.FormatInt(expires, 10) + "." + a.sign(expires)
}

func (a *auth) validSession(session string) bool {
	if session == "" {
		return false
	}
	expires, signature, _ := strings.Cut(session, ".")
	expiresAt, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || expiresAt < time.Now().Unix() {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(signature), []byte(a.sign(expiresAt))) == 1
}

func (a *auth) sign(expires int64) string {
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(strconv.FormatInt(expires, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *auth) loggedIn(request *http.Request) bool {
	cookie, err := request.Cookie(cookieName)
	return err == nil && a.validSession(cookie.Value)
}

func setSessionCookie(w http.ResponseWriter, request *http.Request, session string, lifetime time.Duration) {
	// behind a proxy like Coolify's, the browser talks https to the proxy and the proxy http to us
	forwarded, _, _ := strings.Cut(request.Header.Get("X-Forwarded-Proto"), ",")
	cookie := &http.Cookie{
		Name:     cookieName,
		Value:    session,
		Path:     "/",
		MaxAge:   int(lifetime / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   request.TLS != nil || strings.TrimSpace(forwarded) == "https",
	}
	if lifetime <= 0 {
		// tells the browser to forget it
		cookie.MaxAge = -1
	}
	http.SetCookie(w, cookie)
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

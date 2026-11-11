package web

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
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

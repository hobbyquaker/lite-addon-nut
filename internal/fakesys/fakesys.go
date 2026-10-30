// Package fakesys is the slice of openccu-lite's API the addon talks to, for the tests and the
// page's development: the meta version (the platform probe), the auth state of the token (its
// scopes), and the service control the shutdown goes through, recorded. Plus a fake upsd whose
// variables the tests and the e2e specs set over /dev/ups.
package fakesys

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/hobbyquaker/lite-addon-nut/internal/nut"
)

// Server is the fake system.
type Server struct {
	mu sync.Mutex
	// Token is the one the addon must send; "" accepts any bearer.
	Token string
	// Scopes is what /api/auth/v1/state answers for the token.
	Scopes []string
	// Calls records "<unit> <action>" of every service control.
	Calls []string
	// Refuse makes the service control answer 403 (a token without the right).
	Refuse bool
	// UPSD is the fake upsd the dev routes drive; nil without one.
	UPSD *nut.FakeUPSD
}

// New makes a fake that accepts the token with the scopes.
func New(token string, scopes ...string) *Server {
	return &Server{Token: token, Scopes: scopes}
}

// CallList answers the recorded calls.
func (s *Server) CallList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.Calls...)
}

// Handler is the fake's routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/meta/v1/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"api": "meta", "version": 1, "product": "openccu-lite", "capabilities": map[string]any{"system": 1}})
	})
	mux.HandleFunc("GET /api/auth/v1/state", func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeJSON(w, 401, map[string]any{"error": "unauthenticated", "authenticated": false})
			return
		}
		writeJSON(w, 200, map[string]any{"authenticated": true, "sid": "", "user": "token:addon:nut", "scopes": s.Scopes, "level": "read", "role": "user"})
	})
	mux.HandleFunc("POST /api/system/v1/services/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeJSON(w, 401, map[string]any{"error": "unauthenticated"})
			return
		}
		s.mu.Lock()
		refuse := s.Refuse
		if !refuse {
			s.Calls = append(s.Calls, r.PathValue("id")+" "+r.PathValue("action"))
		}
		s.mu.Unlock()
		if refuse {
			writeJSON(w, 403, map[string]any{"error": "forbidden", "message": "scope system:write needed"})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "output": ""})
	})
	mux.HandleFunc("GET /dev/calls", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.CallList())
	})
	// the dev routes drive the fake upsd: /dev/ups?name=eaton&status=OB+LB&charge=12&runtime=90&stale=1
	mux.HandleFunc("/dev/ups", func(w http.ResponseWriter, r *http.Request) {
		if s.UPSD == nil {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		name := q.Get("name")
		if name == "" {
			name = "eaton"
		}
		for _, k := range []string{"status", "charge", "runtime", "load", "input.voltage"} {
			if v, ok := q[k]; ok {
				vn := map[string]string{"status": "ups.status", "charge": "battery.charge", "runtime": "battery.runtime", "load": "ups.load", "input.voltage": "input.voltage"}[k]
				s.UPSD.Set(name, vn, v[0])
			}
		}
		if v := q.Get("stale"); v != "" {
			s.UPSD.SetStale(name, v == "1")
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /dev/reset", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.Calls = nil
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	return mux
}

func (s *Server) authed(r *http.Request) bool {
	if s.Token == "" {
		return true
	}
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") == s.Token
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

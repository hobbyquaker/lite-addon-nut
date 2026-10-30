// Package httpapi is the addon's own HTTP server: the Svelte page's files and its JSON API under
// one base path (/addons/nut/), which lighttpd proxies to the loopback port. Every request
// carries the shell's session, confirmed through the kit (administrators only); writes carry the
// kit's CSRF header. docs/api.md documents the routes.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit"

	"github.com/hobbyquaker/lite-addon-nut/internal/config"
	"github.com/hobbyquaker/lite-addon-nut/internal/monitor"
	"github.com/hobbyquaker/lite-addon-nut/internal/nut"
)

// RightChecker answers what the addon's token may do (monitor.System; a fake in the tests).
type RightChecker interface {
	CheckRight(ctx context.Context) monitor.Right
}

// Options configure the server.
type Options struct {
	// BasePath is where the page is served, /addons/nut (no trailing slash).
	BasePath string
	// WWW is the directory with the built page.
	WWW     string
	Store   *config.Store
	Monitor *monitor.Monitor
	Events  *monitor.Log
	Right   RightChecker
	// Wrap is the session check; nil: none (development only).
	Wrap    func(http.Handler) http.Handler
	Version string
	Started time.Time
	// Prober connects to a server for the Test button; nut.Probe by default.
	Prober func(ctx context.Context, o nut.Options) (*nut.ProbeResult, error)
}

// Routes lists every "METHOD /path" the API serves; the readme's table is held to it by a test.
var Routes []string

type server struct {
	o       Options
	mu      sync.Mutex
	right   monitor.Right
	rightAt time.Time
}

func handle(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	Routes = append(Routes, pattern)
	mux.HandleFunc(pattern, h)
}

// New builds the handler.
func New(o Options) http.Handler {
	o.BasePath = strings.TrimRight(o.BasePath, "/")
	if o.Prober == nil {
		o.Prober = nut.Probe
	}
	s := &server{o: o}
	Routes = nil
	api := http.NewServeMux()
	handle(api, "GET /api/v1/health", s.health)
	handle(api, "GET /api/v1/config", s.config)
	handle(api, "POST /api/v1/servers", s.putServer)
	handle(api, "PUT /api/v1/servers/{id}", s.putServer)
	handle(api, "DELETE /api/v1/servers/{id}", s.deleteServer)
	handle(api, "POST /api/v1/servers/probe", s.probe)
	handle(api, "POST /api/v1/ups", s.putUPS)
	handle(api, "PUT /api/v1/ups/{id}", s.putUPS)
	handle(api, "DELETE /api/v1/ups/{id}", s.deleteUPS)
	handle(api, "PUT /api/v1/settings", s.putSettings)
	handle(api, "GET /api/v1/status", s.status)
	handle(api, "GET /api/v1/events", s.events)
	handle(api, "DELETE /api/v1/events", s.clearEvents)
	handle(api, "POST /api/v1/poll", s.poll)
	handle(api, "POST /api/v1/shutdown/test", s.test)
	handle(api, "POST /api/v1/shutdown/cancel", s.cancel)
	api.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		addonkit.WriteError(w, http.StatusNotFound, "not-found", r.URL.Path)
	})

	root := http.NewServeMux()
	root.Handle("/api/", addonkit.CSRF(api))
	root.Handle("/", addonkit.AppFiles(o.WWW))

	var h http.Handler = http.StripPrefix(o.BasePath, root)
	if o.Wrap != nil {
		h = o.Wrap(h)
	}
	return guard(h)
}

func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; p != "/" && (path.Clean(p) != strings.TrimSuffix(p, "/") && path.Clean(p) != p) {
			http.NotFound(w, r)
			return
		}
		if strings.Contains(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		addonkit.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, err error) {
	var ve *config.ValidationError
	switch {
	case errors.Is(err, config.ErrNotFound):
		addonkit.WriteError(w, http.StatusNotFound, "not-found", err.Error())
	case errors.As(err, &ve):
		addonkit.WriteError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
	default:
		addonkit.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// GET /api/v1/health: {ok, version, uptime_s, ready}; ?ready=1 answers 503 until the first poll.
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	st := s.o.Monitor.State()
	status := 200
	if r.URL.Query().Get("ready") != "" && !st.Ready {
		status = 503
	}
	writeJSON(w, status, map[string]any{"ok": true, "version": s.o.Version, "uptime_s": int(time.Since(s.o.Started).Seconds()), "ready": st.Ready})
}

// configAnswer is GET /config: the configuration without passwords, the defaults and the right.
type configAnswer struct {
	config.Config
	Defaults struct {
		Settings config.Settings `json:"settings"`
		Rules    config.Rules    `json:"rules"`
	} `json:"defaults"`
	Right   monitor.Right `json:"right"`
	Version string        `json:"version"`
}

func (s *server) answer(w http.ResponseWriter, c config.Config) {
	a := configAnswer{Config: c, Right: s.cachedRight(false), Version: s.o.Version}
	a.Defaults.Settings = config.DefaultSettings
	a.Defaults.Rules = config.DefaultRules
	writeJSON(w, 200, a)
}

// cachedRight asks the system at most once a minute (or when forced).
func (s *server) cachedRight(force bool) monitor.Right {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.o.Right == nil {
		return monitor.Right{Scopes: []string{}}
	}
	if force || !s.right.Checked || time.Since(s.rightAt) > time.Minute {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.right = s.o.Right.CheckRight(ctx)
		cancel()
		s.rightAt = time.Now()
	}
	return s.right
}

func (s *server) config(w http.ResponseWriter, r *http.Request) {
	s.answer(w, s.o.Store.Public())
}

// POST /servers, PUT /servers/{id}: a server; an empty password on an update keeps the old one.
func (s *server) putServer(w http.ResponseWriter, r *http.Request) {
	var sv config.Server
	if !decode(w, r, &sv) {
		return
	}
	if id := r.PathValue("id"); id != "" {
		if _, ok := s.o.Store.Server(id); !ok {
			writeErr(w, config.ErrNotFound)
			return
		}
		sv.ID = id
	} else {
		sv.ID = ""
	}
	c, err := s.o.Store.PutServer(sv)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.o.Monitor.Kick()
	s.answer(w, c)
}

func (s *server) deleteServer(w http.ResponseWriter, r *http.Request) {
	c, err := s.o.Store.DeleteServer(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	s.o.Monitor.Kick()
	s.answer(w, c)
}

// POST /servers/probe: connects with the given settings (an id with an empty password uses the
// stored one) and answers the server's version and its UPS.
func (s *server) probe(w http.ResponseWriter, r *http.Request) {
	var sv config.Server
	if !decode(w, r, &sv) {
		return
	}
	if sv.ID != "" && sv.Password == "" {
		if old, ok := s.o.Store.Server(sv.ID); ok {
			sv.Password = old.Password
		}
	}
	sv.Host = strings.TrimSpace(sv.Host)
	if sv.Host == "" {
		addonkit.WriteError(w, http.StatusUnprocessableEntity, "invalid", "a host is needed")
		return
	}
	if sv.Port == 0 {
		sv.Port = nut.DefaultPort
	}
	if sv.TLS == "" {
		sv.TLS = nut.TLSOff
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, err := s.o.Prober(ctx, monitor.ServerOptions(sv))
	if err != nil {
		var ne *nut.Error
		code := "connect"
		if errors.As(err, &ne) {
			code = "upsd"
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": code, "message": err.Error()})
		return
	}
	writeJSON(w, 200, res)
}

func (s *server) putUPS(w http.ResponseWriter, r *http.Request) {
	var u config.UPS
	if !decode(w, r, &u) {
		return
	}
	if id := r.PathValue("id"); id != "" {
		u.ID = id
		found := false
		for _, x := range s.o.Store.Get().UPS {
			if x.ID == id {
				found = true
			}
		}
		if !found {
			writeErr(w, config.ErrNotFound)
			return
		}
	} else {
		u.ID = ""
	}
	c, err := s.o.Store.PutUPS(u)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.o.Monitor.Kick()
	s.answer(w, c)
}

func (s *server) deleteUPS(w http.ResponseWriter, r *http.Request) {
	c, err := s.o.Store.DeleteUPS(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	s.o.Monitor.Kick()
	s.answer(w, c)
}

func (s *server) putSettings(w http.ResponseWriter, r *http.Request) {
	var st config.Settings
	if !decode(w, r, &st) {
		return
	}
	c, err := s.o.Store.PutSettings(st)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.o.Monitor.Kick()
	s.answer(w, c)
}

// GET /status: the monitor's state and the right.
func (s *server) status(w http.ResponseWriter, r *http.Request) {
	st := s.o.Monitor.State()
	writeJSON(w, 200, map[string]any{"state": st, "right": s.cachedRight(r.URL.Query().Get("recheck") != ""), "now": time.Now().UnixMilli()})
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	ev := s.o.Events.List()
	if ev == nil {
		ev = []monitor.Event{}
	}
	writeJSON(w, 200, ev)
}

func (s *server) clearEvents(w http.ResponseWriter, r *http.Request) {
	s.o.Events.Clear()
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /poll: polls now and answers the state.
func (s *server) poll(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	s.o.Monitor.Poll(ctx)
	s.status(w, r)
}

// POST /shutdown/test {real}: a dry run, or the real shutdown now.
func (s *server) test(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Real bool `json:"real"`
	}
	if r.ContentLength != 0 && !decode(w, r, &body) {
		return
	}
	rec := s.o.Monitor.Test(r.Context(), body.Real)
	writeJSON(w, 200, rec)
}

func (s *server) cancel(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"cancelled": s.o.Monitor.Cancel()})
}

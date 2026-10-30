package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit"

	"github.com/hobbyquaker/lite-addon-nut/internal/config"
	"github.com/hobbyquaker/lite-addon-nut/internal/fakesys"
	"github.com/hobbyquaker/lite-addon-nut/internal/monitor"
	"github.com/hobbyquaker/lite-addon-nut/internal/nut"
)

type world struct {
	t    *testing.T
	ts   *httptest.Server
	sys  *fakesys.Server
	ups  *nut.FakeUPSD
	mon  *monitor.Monitor
	base string
}

// setup wires the whole addon end to end: the fake system (the token holds system:write), a fake
// upsd, the store, the monitor with the real System executor, and the API on a test server.
func setup(t *testing.T, scopes ...string) *world {
	t.Helper()
	ups, err := nut.NewFakeUPSD("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ups.Close() })
	ups.AddUPS("eaton", "Eaton", nut.DemoVars("OL CHRG", 100, 1800))
	ups.AddUser("mon", "pw")
	if scopes == nil {
		scopes = []string{"system:write", "system:read"}
	}
	sys := fakesys.New("tok", scopes...)
	sys.UPSD = ups
	sysTS := httptest.NewServer(sys.Handler())
	t.Cleanup(sysTS.Close)
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	www := t.TempDir()
	_ = os.WriteFile(filepath.Join(www, "index.html"), []byte("<!doctype html><title>NUT</title>"), 0o644)
	exec := &monitor.System{Base: sysTS.URL, HTTP: &http.Client{Transport: &addonkit.AuthTransport{Source: addonkit.StaticToken("tok")}}}
	events := monitor.NewLog("", 50)
	mon := monitor.New(monitor.Options{Store: store, Executor: exec, Events: events, Log: t.Logf})
	store.OnChange = mon.Kick
	h := New(Options{BasePath: "/addons/nut", WWW: www, Store: store, Monitor: mon, Events: events, Right: exec, Version: "test", Started: time.Now()})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &world{t: t, ts: ts, sys: sys, ups: ups, mon: mon, base: ts.URL + "/addons/nut/"}
}

func (w *world) call(method, path string, body any, csrf bool) (int, map[string]any, []byte) {
	w.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, w.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set("X-CCU-Addon-Request", "1")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return res.StatusCode, m, raw
}

func TestEndToEnd(t *testing.T) {
	w := setup(t)

	// the page's files and the health
	st, _, raw := w.call("GET", "", nil, false)
	if st != 200 || !strings.Contains(string(raw), "<title>NUT</title>") {
		t.Fatalf("index: %d %s", st, raw)
	}
	if st, _, _ = w.call("GET", "api/v1/health?ready=1", nil, false); st != 503 {
		t.Errorf("not ready yet: %d", st)
	}

	// a write without the CSRF header is refused
	if st, _, _ = w.call("POST", "api/v1/servers", map[string]any{"host": "x"}, false); st != 403 {
		t.Errorf("csrf: %d", st)
	}

	// the probe: a server that refuses reads without a login, then the login
	w.ups.RequireLogin = true
	st, m, _ := w.call("POST", "api/v1/servers/probe", map[string]any{"host": "127.0.0.1", "port": w.ups.Port(), "username": "mon", "password": "nope"}, true)
	if st != 502 || m["error"] != "upsd" || !strings.Contains(m["message"].(string), "ACCESS-DENIED") {
		t.Errorf("probe wrong pw: %d %v", st, m)
	}
	st, m, _ = w.call("POST", "api/v1/servers/probe", map[string]any{"host": "127.0.0.1", "port": w.ups.Port(), "username": "mon", "password": "pw"}, true)
	if st != 200 || len(m["ups"].([]any)) != 1 || m["ups"].([]any)[0].(map[string]any)["name"] != "eaton" {
		t.Errorf("probe: %d %v", st, m)
	}
	st, m, _ = w.call("POST", "api/v1/servers/probe", map[string]any{"host": "127.0.0.1", "port": 1}, true)
	if st != 502 || m["error"] != "connect" {
		t.Errorf("probe refused port: %d %v", st, m)
	}

	// a server and a UPS
	st, m, _ = w.call("POST", "api/v1/servers", map[string]any{"name": "lab", "host": "127.0.0.1", "port": w.ups.Port(), "username": "mon", "password": "pw", "tls": "off"}, true)
	if st != 200 {
		t.Fatalf("server: %d %v", st, m)
	}
	servers := m["servers"].([]any)
	srv := servers[0].(map[string]any)
	if srv["password"] != nil || srv["id"] != "srv1" {
		t.Errorf("server answer: %v", srv)
	}
	if m["right"].(map[string]any)["ok"] != true {
		t.Errorf("right: %v", m["right"])
	}
	// the probe with the stored password (id, no password)
	if st, m, _ = w.call("POST", "api/v1/servers/probe", map[string]any{"id": "srv1", "host": "127.0.0.1", "port": w.ups.Port(), "username": "mon"}, true); st != 200 {
		t.Errorf("probe stored: %d %v", st, m)
	}
	st, m, _ = w.call("POST", "api/v1/ups", map[string]any{"server": "srv1", "name": "eaton", "label": "Keller", "enabled": true, "rules": config.DefaultRules}, true)
	if st != 200 || len(m["ups"].([]any)) != 1 {
		t.Fatalf("ups: %d %v", st, m)
	}
	if st, m, _ = w.call("POST", "api/v1/ups", map[string]any{"server": "nope", "name": "x"}, true); st != 422 {
		t.Errorf("unknown server: %d %v", st, m)
	}
	if st, _, _ = w.call("PUT", "api/v1/ups/ups9", map[string]any{"server": "srv1", "name": "x"}, true); st != 404 {
		t.Errorf("unknown ups: %d", st)
	}
	// the settings: a dry run with no grace
	st, m, _ = w.call("PUT", "api/v1/settings", map[string]any{"enabled": true, "action": "poweroff", "dry_run": true, "grace_s": 0, "poll_s": 1, "dead_s": 30}, true)
	if st != 200 || m["settings"].(map[string]any)["dry_run"] != true {
		t.Fatalf("settings: %d %v", st, m)
	}

	// a poll: the UPS is read
	st, m, _ = w.call("POST", "api/v1/poll", nil, true)
	state := m["state"].(map[string]any)
	ups := state["ups"].([]any)[0].(map[string]any)
	if st != 200 || ups["connected"] != true || ups["charge"].(float64) != 100 || ups["label"] != "Keller" {
		t.Fatalf("poll: %d %v", st, ups)
	}
	if st, _, _ = w.call("GET", "api/v1/health?ready=1", nil, false); st != 200 {
		t.Errorf("ready: %d", st)
	}

	// low battery, dry run: no call to the system, a dry_run event
	w.ups.Set("eaton", "ups.status", "OB DISCHRG LB")
	w.ups.Set("eaton", "battery.charge", "7")
	_, m, _ = w.call("POST", "api/v1/poll", nil, true)
	state = m["state"].(map[string]any)
	if sd := state["shutdown"]; sd == nil || sd.(map[string]any)["dry_run"] != true {
		t.Fatalf("dry run: %v", state)
	}
	if len(w.sys.CallList()) != 0 {
		t.Fatal("a dry run called the system")
	}
	_, _, raw = w.call("GET", "api/v1/events", nil, false)
	var events []monitor.Event
	_ = json.Unmarshal(raw, &events)
	if len(events) == 0 || events[len(events)-1].Kind != monitor.KindDryRun {
		t.Errorf("events: %+v", events)
	}

	// the Test button for real: the system gets systemctl start poweroff.target
	st, m, _ = w.call("POST", "api/v1/shutdown/test", map[string]any{"real": true}, true)
	if st != 200 || m["error"] != nil {
		t.Fatalf("test real: %d %v", st, m)
	}
	if calls := w.sys.CallList(); len(calls) != 1 || calls[0] != "poweroff.target start" {
		t.Fatalf("calls %v", calls)
	}

	// the config answer hides the password and carries the defaults
	_, m, _ = w.call("GET", "api/v1/config", nil, false)
	if m["servers"].([]any)[0].(map[string]any)["password"] != nil || m["defaults"].(map[string]any)["rules"].(map[string]any)["low_battery"] != true {
		t.Errorf("config: %v", m)
	}

	// deleting the server takes the UPS
	st, m, _ = w.call("DELETE", "api/v1/servers/srv1", nil, true)
	if st != 200 || len(m["ups"].([]any)) != 0 {
		t.Errorf("delete: %d %v", st, m)
	}
	if st, _, _ = w.call("DELETE", "api/v1/servers/srv1", nil, true); st != 404 {
		t.Errorf("delete twice: %d", st)
	}
	if st, _, _ = w.call("DELETE", "api/v1/events", nil, true); st != 200 {
		t.Errorf("clear events: %d", st)
	}
	_, _, raw = w.call("GET", "api/v1/events", nil, false)
	if strings.TrimSpace(string(raw)) != "[]" {
		t.Errorf("events after clear: %s", raw)
	}
}

func TestRealShutdownRefused(t *testing.T) {
	// the token lacks system:write: the right says so and the system refuses the call
	w := setup(t, "system:read")
	_, m, _ := w.call("GET", "api/v1/config", nil, false)
	if r := m["right"].(map[string]any); r["ok"] != false || r["checked"] != true {
		t.Errorf("right: %v", r)
	}
	w.sys.Refuse = true
	st, m, _ := w.call("POST", "api/v1/shutdown/test", map[string]any{"real": true}, true)
	if st != 200 || !strings.Contains(m["error"].(string), "403") {
		t.Errorf("refused: %d %v", st, m)
	}
	if len(w.sys.CallList()) != 0 {
		t.Error("recorded a refused call")
	}
}

func TestCountdownAndCancel(t *testing.T) {
	w := setup(t)
	w.call("POST", "api/v1/servers", map[string]any{"name": "lab", "host": "127.0.0.1", "port": w.ups.Port(), "username": "mon", "password": "pw"}, true)
	w.call("POST", "api/v1/ups", map[string]any{"server": "srv1", "name": "eaton", "enabled": true, "rules": config.DefaultRules}, true)
	w.call("PUT", "api/v1/settings", map[string]any{"enabled": true, "action": "reboot", "grace_s": 600, "poll_s": 1, "dead_s": 30}, true)
	w.call("POST", "api/v1/poll", nil, true)
	w.ups.Set("eaton", "ups.status", "OB LB")
	_, m, _ := w.call("POST", "api/v1/poll", nil, true)
	cd := m["state"].(map[string]any)["countdown"]
	if cd == nil || cd.(map[string]any)["action"] != "reboot" {
		t.Fatalf("countdown: %v", m["state"])
	}
	if st, m, _ := w.call("POST", "api/v1/shutdown/cancel", nil, true); st != 200 || m["cancelled"] != true {
		t.Errorf("cancel: %d %v", st, m)
	}
	_, m, _ = w.call("GET", "api/v1/status", nil, false)
	state := m["state"].(map[string]any)
	if state["countdown"] != nil || state["ups"].([]any)[0].(map[string]any)["suppressed"] != true {
		t.Errorf("after cancel: %v", state)
	}
	if len(w.sys.CallList()) != 0 {
		t.Error("the system was called")
	}
}

// Every route is documented in docs/api.md.
func TestRoutesDocumented(t *testing.T) {
	setup(t)
	doc, err := os.ReadFile("../../docs/api.md")
	if err != nil {
		t.Skip("docs/api.md not there")
	}
	for _, r := range Routes {
		method, p, _ := strings.Cut(r, " ")
		if !strings.Contains(string(doc), "`"+method+" "+strings.TrimPrefix(p, "/")+"`") && !strings.Contains(string(doc), "`"+method+" "+strings.TrimPrefix(p, "/")) {
			t.Errorf("%s is not in docs/api.md", r)
		}
	}
}

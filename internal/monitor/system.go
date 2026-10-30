package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// System shuts the system down through openccu-lite's API. An addon token never holds the
// `power` scope (the system strips it), so the addon cannot call POST /reboot or POST /halt; what
// it can hold is `system:write`, whose service control starts a systemd unit by name:
// POST /api/system/v1/services/poweroff.target/start (or reboot.target). That is the narrowest
// right that works today.
type System struct {
	// Base is the system's URL, http://127.0.0.1 by default.
	Base string
	// HTTP carries the addon's token (addonkit.AuthTransport).
	HTTP *http.Client
	Log  func(format string, args ...any)
}

// Units per action.
var units = map[string]string{"poweroff": "poweroff.target", "reboot": "reboot.target"}

// Shutdown starts the target.
func (s *System) Shutdown(ctx context.Context, action string) error {
	unit, ok := units[action]
	if !ok {
		return fmt.Errorf("unknown action %q", action)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", s.base()+"/api/system/v1/services/"+unit+"/start", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	if res.StatusCode != 200 {
		var e struct {
			Error, Message string
		}
		_ = json.Unmarshal(body, &e)
		if e.Error != "" {
			return fmt.Errorf("the system refused: %d %s (%s)", res.StatusCode, e.Error, e.Message)
		}
		return fmt.Errorf("the system refused: %d %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	if s.Log != nil {
		s.Log("the system accepted: systemctl start %s", unit)
	}
	return nil
}

// Right is what the addon's token may do: whether it holds system:write (the shutdown right).
type Right struct {
	// Checked is false until the system answered once.
	Checked bool     `json:"checked"`
	OK      bool     `json:"ok"`
	Scopes  []string `json:"scopes"`
	Error   string   `json:"error,omitempty"`
	User    string   `json:"user,omitempty"`
}

// CheckRight asks the system what the token holds.
func (s *System) CheckRight(ctx context.Context) Right {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", s.base()+"/api/auth/v1/state", nil)
	if err != nil {
		return Right{Checked: true, Error: err.Error(), Scopes: []string{}}
	}
	res, err := s.HTTP.Do(req)
	if err != nil {
		return Right{Checked: true, Error: err.Error(), Scopes: []string{}}
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	if res.StatusCode != 200 {
		return Right{Checked: true, Error: fmt.Sprintf("%d %s", res.StatusCode, strings.TrimSpace(string(body))), Scopes: []string{}}
	}
	var st struct {
		Authenticated bool     `json:"authenticated"`
		User          string   `json:"user"`
		Scopes        []string `json:"scopes"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		return Right{Checked: true, Error: err.Error(), Scopes: []string{}}
	}
	r := Right{Checked: true, Scopes: st.Scopes, User: st.User}
	if r.Scopes == nil {
		r.Scopes = []string{}
	}
	for _, sc := range st.Scopes {
		if sc == "system:write" || sc == "*" {
			r.OK = true
		}
	}
	if !st.Authenticated {
		r.Error = "the token is not accepted"
	}
	return r
}

// Await waits until the system answers (at boot its web server answers 503 until occulited is up)
// and the token is accepted. It returns the right it found.
func (s *System) Await(ctx context.Context) (Right, error) {
	delay := time.Second
	for {
		r := s.CheckRight(ctx)
		if r.Error == "" {
			return r, nil
		}
		if s.Log != nil {
			s.Log("waiting for the system: %s", r.Error)
		}
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 15*time.Second {
			delay *= 2
		}
	}
}

func (s *System) base() string {
	if s.Base == "" {
		return "http://127.0.0.1"
	}
	return strings.TrimRight(s.Base, "/")
}

// ErrNoExecutor is answered when the monitor has no executor (a test).
var ErrNoExecutor = errors.New("no executor")

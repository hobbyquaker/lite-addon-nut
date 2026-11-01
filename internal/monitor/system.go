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

// System powers the system off through openccu-lite's API: POST /api/system/v1/halt, for which
// the addon's token holds `power:halt` (openccu-lite 1.1.0-beta.1), that route and nothing else.
type System struct {
	// Base is the system's URL, http://127.0.0.1 by default.
	Base string
	// HTTP carries the addon's token (addonkit.AuthTransport).
	HTTP *http.Client
	Log  func(format string, args ...any)
}

// Shutdown orders the power-off.
func (s *System) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", s.base()+"/api/system/v1/halt", bytes.NewReader([]byte(`{"confirm":true}`)))
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
		s.Log("the system accepted the power-off")
	}
	return nil
}

// Right is what the addon's token may do: whether it holds power:halt (the shutdown right).
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
		if sc == "power:halt" || sc == "power" || sc == "*" {
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

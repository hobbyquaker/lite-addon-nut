// Package config is the addon's configuration: the upsd servers (with their passwords), the UPS
// watched on them with their rules, and the shutdown settings. One JSON file, nut.json, in the
// configuration directory the system gives the addon (0700); the file is 0600 as it holds the
// upsd passwords. Reads are copies; a write validates, writes a temporary file and renames.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Server is one upsd.
type Server struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	// Password is write-only on the API: a saved server's password is never answered.
	Password string `json:"password,omitempty"`
	// TLS is "off" or "starttls".
	TLS string `json:"tls"`
	// TLSVerify false accepts any certificate (a self-signed upsd without a CA).
	TLSVerify bool `json:"tls_verify"`
	// CACert is a PEM bundle the server's certificate must chain to (optional).
	CACert string `json:"ca_cert,omitempty"`
}

// Rules are the conditions that shut the system down, per UPS. A condition with a zero
// threshold or a false switch is off.
type Rules struct {
	// LowBattery: the UPS says LB while on battery.
	LowBattery bool `json:"low_battery"`
	// FSD: the master ordered a forced shutdown. No grace period.
	FSD bool `json:"fsd"`
	// ChargeBelow: on battery and battery.charge below this percentage.
	ChargeBelow int `json:"charge_below"`
	// RuntimeBelow: on battery and battery.runtime below this many seconds.
	RuntimeBelow int `json:"runtime_below"`
	// OnBatteryLonger: on battery for at least this many seconds.
	OnBatteryLonger int `json:"on_battery_longer"`
	// CommLost: the server stopped answering (or the UPS went stale) while the UPS was last seen
	// on battery - upsmon's rule - for at least Settings.DeadS seconds.
	CommLost bool `json:"comm_lost"`
}

// UPS is one UPS watched on a server.
type UPS struct {
	ID      string `json:"id"`
	Server  string `json:"server"`
	Name    string `json:"name"` // the name on the server (upsname)
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
	Rules   Rules  `json:"rules"`
}

// Settings are the shutdown settings.
type Settings struct {
	// Enabled switches the watch on; off, the page shows the UPS and nothing is ever shut down.
	Enabled bool `json:"enabled"`
	// DryRun logs and shows what would happen and never calls the system.
	DryRun bool `json:"dry_run"`
	// GraceS is the countdown between the condition and the shutdown; cancelled when mains
	// returns in time. FSD ignores it.
	GraceS int `json:"grace_s"`
	// PollS is the polling interval.
	PollS int `json:"poll_s"`
	// DeadS is how long the data may be missing before CommLost applies.
	DeadS int `json:"dead_s"`
	// RequireAll: every enabled UPS must be critical (a system fed by two UPS); default any.
	RequireAll bool `json:"require_all"`
}

// Config is the whole file.
type Config struct {
	Servers  []Server `json:"servers"`
	UPS      []UPS    `json:"ups"`
	Settings Settings `json:"settings"`
}

// Defaults for the settings and a new UPS's rules.
var (
	DefaultSettings = Settings{Enabled: true, DryRun: false, GraceS: 30, PollS: 5, DeadS: 60}
	DefaultRules    = Rules{LowBattery: true, FSD: true, ChargeBelow: 0, RuntimeBelow: 0, OnBatteryLonger: 0, CommLost: true}
)

// ActionPoweroff is the only thing the shutdown does; the state and the events still name it, and
// events of earlier versions may name "reboot".
const ActionPoweroff = "poweroff"

// ErrNotFound is answered for an unknown id.
var ErrNotFound = errors.New("not found")

// ValidationError is a refused change.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
var upsNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// Store holds the configuration and the file.
type Store struct {
	rebootRemoved bool
	mu            sync.RWMutex
	path          string
	c             Config
	OnChange      func()
}

// Open reads nut.json from dir (made when missing).
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "nut.json"), c: Config{Settings: DefaultSettings}}
	b, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	c.normalize()
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	s.c = c
	// a file of 0.1.x names its action: the key goes for good, and a reboot is reported once
	var old struct {
		Settings struct {
			Action *string `json:"action"`
		} `json:"settings"`
	}
	if json.Unmarshal(b, &old) == nil && old.Settings.Action != nil {
		s.rebootRemoved = *old.Settings.Action == "reboot"
		if err := write(s.path, c); err != nil {
			return nil, fmt.Errorf("%s: %w", s.path, err)
		}
	}
	return s, nil
}

// RebootRemoved says this start migrated a configuration whose action was a reboot: from now on
// the shutdown powers the system off, and only this start can tell the user so.
func (s *Store) RebootRemoved() bool { return s.rebootRemoved }

// Get answers a copy of the configuration, passwords included (for the monitor).
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.c.clone()
}

// Public answers a copy without the passwords (for the page).
func (s *Store) Public() Config {
	c := s.Get()
	for i := range c.Servers {
		c.Servers[i].Password = ""
	}
	return c
}

// Server finds one, passwords included.
func (s *Store) Server(id string) (Server, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sv := range s.c.Servers {
		if sv.ID == id {
			return sv, true
		}
	}
	return Server{}, false
}

// PutServer adds or replaces a server. An empty password on an existing server keeps the old one.
func (s *Store) PutServer(sv Server) (Config, error) {
	return s.update(func(c *Config) error {
		sv.Host = strings.TrimSpace(sv.Host)
		sv.Name = strings.TrimSpace(sv.Name)
		sv.Username = strings.TrimSpace(sv.Username)
		if sv.ID == "" {
			sv.ID = newID("srv", func(id string) bool { return c.serverIndex(id) >= 0 })
		}
		if i := c.serverIndex(sv.ID); i >= 0 {
			if sv.Password == "" {
				sv.Password = c.Servers[i].Password
			}
			c.Servers[i] = sv
		} else {
			c.Servers = append(c.Servers, sv)
		}
		return nil
	})
}

// DeleteServer removes a server and the UPS on it.
func (s *Store) DeleteServer(id string) (Config, error) {
	return s.update(func(c *Config) error {
		i := c.serverIndex(id)
		if i < 0 {
			return ErrNotFound
		}
		c.Servers = append(c.Servers[:i], c.Servers[i+1:]...)
		kept := c.UPS[:0]
		for _, u := range c.UPS {
			if u.Server != id {
				kept = append(kept, u)
			}
		}
		c.UPS = kept
		return nil
	})
}

// PutUPS adds or replaces a UPS.
func (s *Store) PutUPS(u UPS) (Config, error) {
	return s.update(func(c *Config) error {
		u.Name = strings.TrimSpace(u.Name)
		u.Label = strings.TrimSpace(u.Label)
		if u.ID == "" {
			u.ID = newID("ups", func(id string) bool { return c.upsIndex(id) >= 0 })
		}
		if i := c.upsIndex(u.ID); i >= 0 {
			c.UPS[i] = u
		} else {
			c.UPS = append(c.UPS, u)
		}
		return nil
	})
}

// DeleteUPS removes a UPS.
func (s *Store) DeleteUPS(id string) (Config, error) {
	return s.update(func(c *Config) error {
		i := c.upsIndex(id)
		if i < 0 {
			return ErrNotFound
		}
		c.UPS = append(c.UPS[:i], c.UPS[i+1:]...)
		return nil
	})
}

// PutSettings replaces the settings.
func (s *Store) PutSettings(st Settings) (Config, error) {
	return s.update(func(c *Config) error {
		c.Settings = st
		return nil
	})
}

func (s *Store) update(f func(c *Config) error) (Config, error) {
	s.mu.Lock()
	c := s.c.clone()
	if err := f(&c); err != nil {
		s.mu.Unlock()
		return Config{}, err
	}
	c.normalize()
	if err := c.validate(); err != nil {
		s.mu.Unlock()
		return Config{}, err
	}
	if err := write(s.path, c); err != nil {
		s.mu.Unlock()
		return Config{}, err
	}
	s.c = c
	cb := s.OnChange
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
	pub := c.clone()
	for i := range pub.Servers {
		pub.Servers[i].Password = ""
	}
	return pub, nil
}

func write(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c Config) clone() Config {
	b, _ := json.Marshal(c)
	var out Config
	_ = json.Unmarshal(b, &out)
	if out.Servers == nil {
		out.Servers = []Server{}
	}
	if out.UPS == nil {
		out.UPS = []UPS{}
	}
	return out
}

func (c *Config) serverIndex(id string) int {
	for i, s := range c.Servers {
		if s.ID == id {
			return i
		}
	}
	return -1
}

func (c *Config) upsIndex(id string) int {
	for i, u := range c.UPS {
		if u.ID == id {
			return i
		}
	}
	return -1
}

func (c *Config) normalize() {
	if c.Servers == nil {
		c.Servers = []Server{}
	}
	if c.UPS == nil {
		c.UPS = []UPS{}
	}
	for i := range c.Servers {
		if c.Servers[i].Port == 0 {
			c.Servers[i].Port = 3493
		}
		if c.Servers[i].TLS == "" {
			c.Servers[i].TLS = "off"
		}
		if c.Servers[i].Name == "" {
			c.Servers[i].Name = c.Servers[i].Host
		}
	}
	for i := range c.UPS {
		if c.UPS[i].Label == "" {
			c.UPS[i].Label = c.UPS[i].Name
		}
	}
	if c.Settings.PollS == 0 {
		c.Settings.PollS = DefaultSettings.PollS
	}
	if c.Settings.DeadS == 0 {
		c.Settings.DeadS = DefaultSettings.DeadS
	}
	sort.SliceStable(c.Servers, func(i, j int) bool { return c.Servers[i].Name < c.Servers[j].Name })
}

func (c *Config) validate() error {
	seen := map[string]bool{}
	for _, s := range c.Servers {
		if !idRe.MatchString(s.ID) {
			return invalid("server id %q", s.ID)
		}
		if seen[s.ID] {
			return invalid("server id %q twice", s.ID)
		}
		seen[s.ID] = true
		if s.Host == "" || strings.ContainsAny(s.Host, " /\\\"'\t\n") {
			return invalid("a server needs a host name or address")
		}
		if s.Port < 1 || s.Port > 65535 {
			return invalid("port %d", s.Port)
		}
		if s.TLS != "off" && s.TLS != "starttls" {
			return invalid("tls must be off or starttls")
		}
		if (s.Username == "") != (s.Password == "") {
			return invalid("a user name needs a password, and a password a user name")
		}
		if strings.ContainsAny(s.Username+s.Password, " \t\r\n\"") {
			return invalid("the user name and the password cannot hold spaces or quotes")
		}
		if len(s.Name) > 64 || len(s.Username) > 64 || len(s.Password) > 128 || len(s.CACert) > 64*1024 {
			return invalid("too long")
		}
	}
	seenUPS := map[string]bool{}
	for _, u := range c.UPS {
		if !idRe.MatchString(u.ID) {
			return invalid("ups id %q", u.ID)
		}
		if seenUPS[u.ID] {
			return invalid("ups id %q twice", u.ID)
		}
		seenUPS[u.ID] = true
		if !seen[u.Server] {
			return invalid("ups %q names an unknown server %q", u.Name, u.Server)
		}
		if !upsNameRe.MatchString(u.Name) {
			return invalid("ups name %q", u.Name)
		}
		if len(u.Label) > 64 {
			return invalid("label too long")
		}
		r := u.Rules
		if r.ChargeBelow < 0 || r.ChargeBelow > 100 || r.RuntimeBelow < 0 || r.RuntimeBelow > 86400 || r.OnBatteryLonger < 0 || r.OnBatteryLonger > 86400 {
			return invalid("a threshold is out of range")
		}
	}
	st := c.Settings
	if st.GraceS < 0 || st.GraceS > 3600 {
		return invalid("the grace period is 0 to 3600 s")
	}
	if st.PollS < 1 || st.PollS > 300 {
		return invalid("the polling interval is 1 to 300 s")
	}
	if st.DeadS < 5 || st.DeadS > 3600 {
		return invalid("the dead time is 5 to 3600 s")
	}
	return nil
}

func newID(prefix string, taken func(string) bool) string {
	for i := 1; ; i++ {
		id := fmt.Sprintf("%s%d", prefix, i)
		if !taken(id) {
			return id
		}
	}
}

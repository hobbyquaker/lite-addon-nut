// Package monitor polls the configured UPS on their upsd servers, applies the rules, runs the
// grace countdown and shuts the system down through the Executor. It keeps one connection per
// server and reconnects when one fails; every poll is one LIST VAR per UPS.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/lite-addon-nut/internal/config"
	"github.com/hobbyquaker/lite-addon-nut/internal/nut"
)

// Executor performs the shutdown. The real one calls the system's API; a test records.
type Executor interface {
	// Shutdown runs the action (config.ActionPoweroff or ActionReboot).
	Shutdown(ctx context.Context, action string) error
}

// The rules a reason names.
const (
	ReasonLowBattery = "low_battery"
	ReasonFSD        = "fsd"
	ReasonCharge     = "charge_below"
	ReasonRuntime    = "runtime_below"
	ReasonDuration   = "on_battery_longer"
	ReasonCommLost   = "comm_lost"
)

// Reason is one rule that fired on one UPS.
type Reason struct {
	Rule      string  `json:"rule"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
}

// UPSState is what the page shows per UPS.
type UPSState struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Server    string   `json:"server"`
	ServerKey string   `json:"server_name"`
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Connected bool     `json:"connected"`
	Error     string   `json:"error,omitempty"`
	LastOK    *int64   `json:"last_ok,omitempty"` // unix ms
	DataAgeS  *int     `json:"data_age_s,omitempty"`
	Status    []string `json:"status"`
	OnBattery bool     `json:"on_battery"`
	// OnBatteryS is how long the UPS has been on battery, -1 when it is not.
	OnBatteryS   int               `json:"on_battery_s"`
	Charge       *float64          `json:"charge,omitempty"`
	Runtime      *float64          `json:"runtime,omitempty"`
	Load         *float64          `json:"load,omitempty"`
	InputVoltage *float64          `json:"input_voltage,omitempty"`
	OutputVolt   *float64          `json:"output_voltage,omitempty"`
	BatteryVolt  *float64          `json:"battery_voltage,omitempty"`
	Temperature  *float64          `json:"temperature,omitempty"`
	Model        string            `json:"model,omitempty"`
	Mfr          string            `json:"mfr,omitempty"`
	Serial       string            `json:"serial,omitempty"`
	Vars         map[string]string `json:"vars,omitempty"`
	Reasons      []Reason          `json:"reasons"`
	Suppressed   bool              `json:"suppressed"`
	Rules        config.Rules      `json:"rules"`
}

// Countdown is the running grace period.
type Countdown struct {
	Since   int64    `json:"since"` // unix ms
	Until   int64    `json:"until"`
	Reasons []string `json:"reasons"` // "<label>: <rule>"
	DryRun  bool     `json:"dry_run"`
	Action  string   `json:"action"`
}

// Shutdown is the record of the last shutdown issued (or dry run).
type Shutdown struct {
	At      int64    `json:"at"`
	Action  string   `json:"action"`
	DryRun  bool     `json:"dry_run"`
	Test    bool     `json:"test"`
	Reasons []string `json:"reasons"`
	Error   string   `json:"error,omitempty"`
}

// State is the page's status answer.
type State struct {
	Enabled    bool       `json:"enabled"`
	DryRun     bool       `json:"dry_run"`
	Action     string     `json:"action"`
	RequireAll bool       `json:"require_all"`
	GraceS     int        `json:"grace_s"`
	UPS        []UPSState `json:"ups"`
	Countdown  *Countdown `json:"countdown,omitempty"`
	Shutdown   *Shutdown  `json:"shutdown,omitempty"`
	Issued     bool       `json:"issued"`
	Polls      int64      `json:"polls"`
	LastPoll   *int64     `json:"last_poll,omitempty"`
	Ready      bool       `json:"ready"`
}

// Options configure the monitor.
type Options struct {
	Store    *config.Store
	Executor Executor
	Events   *Log
	Log      func(format string, args ...any)
	// Now is the clock; time.Now by default.
	Now func() time.Time
	// Dialer makes the connections; nut.Dial by default.
	Dialer func(ctx context.Context, o nut.Options) (*nut.Conn, error)
	// OnChange is called after every poll and every event (the page's stream).
	OnChange func()
}

type upsRun struct {
	st             UPSState
	lastStatus     *nut.Status
	onBatterySince time.Time
	lastOK         time.Time
	armed          bool // false after a dry run until the condition clears
}

// Monitor is the poller.
type Monitor struct {
	o     Options
	mu    sync.Mutex
	conns map[string]*nut.Conn
	ups   map[string]*upsRun
	cd    *countdown
	last  *Shutdown
	// issued is set once a real shutdown was ordered; nothing more is done then
	issued   bool
	polls    int64
	lastPoll time.Time
	ready    bool
	retryAt  time.Time
	wake     chan struct{}
}

type countdown struct {
	since, until time.Time
	reasons      []string
	dryRun       bool
	action       string
	fsd          bool
}

// New makes a monitor.
func New(o Options) *Monitor {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Dialer == nil {
		o.Dialer = nut.Dial
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if o.Events == nil {
		o.Events = NewLog("", 200)
	}
	return &Monitor{o: o, conns: map[string]*nut.Conn{}, ups: map[string]*upsRun{}, wake: make(chan struct{}, 1)}
}

// Kick polls at once (after a configuration change).
func (m *Monitor) Kick() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Run polls until the context ends.
func (m *Monitor) Run(ctx context.Context) {
	for {
		m.Poll(ctx)
		c := m.o.Store.Get()
		wait := time.Duration(c.Settings.PollS) * time.Second
		// a running countdown is checked every second
		m.mu.Lock()
		if m.cd != nil && wait > time.Second {
			wait = time.Second
		}
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			m.closeAll()
			return
		case <-time.After(wait):
		case <-m.wake:
		}
	}
}

func (m *Monitor) closeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, c := range m.conns {
		c.Close()
		delete(m.conns, k)
	}
}

// Poll reads every UPS once and applies the rules.
func (m *Monitor) Poll(ctx context.Context) {
	c := m.o.Store.Get()
	now := m.o.Now()
	byServer := map[string]config.Server{}
	for _, s := range c.Servers {
		byServer[s.ID] = s
	}
	// drop runs of UPS that are gone
	m.mu.Lock()
	known := map[string]bool{}
	for _, u := range c.UPS {
		known[u.ID] = true
	}
	for id := range m.ups {
		if !known[id] {
			delete(m.ups, id)
		}
	}
	for id := range m.conns {
		if _, ok := byServer[id]; !ok {
			m.conns[id].Close()
			delete(m.conns, id)
		}
	}
	m.mu.Unlock()

	for _, u := range c.UPS {
		sv := byServer[u.Server]
		m.pollUPS(ctx, c.Settings, sv, u, now)
	}
	m.decide(ctx, c, now)
	m.mu.Lock()
	m.polls++
	m.lastPoll = now
	m.ready = true
	m.mu.Unlock()
	if m.o.OnChange != nil {
		m.o.OnChange()
	}
}

func (m *Monitor) conn(ctx context.Context, sv config.Server) (*nut.Conn, error) {
	m.mu.Lock()
	c := m.conns[sv.ID]
	m.mu.Unlock()
	if c != nil {
		return c, nil
	}
	c, err := m.o.Dialer(ctx, serverOptions(sv))
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.conns[sv.ID] = c
	m.mu.Unlock()
	return c, nil
}

func (m *Monitor) dropConn(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c := m.conns[id]; c != nil {
		c.Close()
		delete(m.conns, id)
	}
}

// serverOptions turns a configured server into dial options.
func serverOptions(sv config.Server) nut.Options {
	return nut.Options{Host: sv.Host, Port: sv.Port, Username: sv.Username, Password: sv.Password, TLS: sv.TLS, InsecureSkipVerify: !sv.TLSVerify, CACert: sv.CACert, Timeout: 8 * time.Second}
}

// ServerOptions is serverOptions for the API's probe.
func ServerOptions(sv config.Server) nut.Options { return serverOptions(sv) }

func (m *Monitor) pollUPS(ctx context.Context, st config.Settings, sv config.Server, u config.UPS, now time.Time) {
	m.mu.Lock()
	r := m.ups[u.ID]
	if r == nil {
		r = &upsRun{armed: true}
		m.ups[u.ID] = r
	}
	prev := r.st
	r.st.ID, r.st.Label, r.st.Server, r.st.ServerKey, r.st.Name, r.st.Enabled, r.st.Rules = u.ID, u.Label, u.Server, sv.Name, u.Name, u.Enabled, u.Rules
	m.mu.Unlock()

	vars, err := m.readVars(ctx, sv, u.Name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		r.st.Connected = false
		r.st.Error = err.Error()
		if prev.Connected {
			m.o.Events.Add(Event{At: now, UPS: u.ID, Label: u.Label, Kind: KindCommLost, Message: err.Error()})
		}
	} else {
		r.st.Connected = true
		r.st.Error = ""
		r.lastOK = now
		r.st.Vars = vars
		status := nut.ParseStatus(vars["ups.status"])
		r.st.Status = status.Flags
		if r.st.Status == nil {
			r.st.Status = []string{}
		}
		r.st.Charge = floatPtr(vars, "battery.charge")
		r.st.Runtime = floatPtr(vars, "battery.runtime")
		r.st.Load = floatPtr(vars, "ups.load")
		r.st.InputVoltage = floatPtr(vars, "input.voltage")
		r.st.OutputVolt = floatPtr(vars, "output.voltage")
		r.st.BatteryVolt = floatPtr(vars, "battery.voltage")
		r.st.Temperature = floatPtr(vars, "ups.temperature")
		r.st.Model = first(vars, "ups.model", "device.model")
		r.st.Mfr = first(vars, "ups.mfr", "device.mfr")
		r.st.Serial = first(vars, "ups.serial", "device.serial")
		if !prev.Connected && prev.LastOK != nil {
			m.o.Events.Add(Event{At: now, UPS: u.ID, Label: u.Label, Kind: KindCommBack})
		} else if !prev.Connected && prev.LastOK == nil {
			m.o.Events.Add(Event{At: now, UPS: u.ID, Label: u.Label, Kind: KindConnected, Status: status.String()})
		}
		if r.lastStatus == nil || r.lastStatus.String() != status.String() {
			if r.lastStatus != nil {
				m.o.Events.Add(Event{At: now, UPS: u.ID, Label: u.Label, Kind: KindStatus, Status: status.String(), Prev: r.lastStatus.String(), Charge: r.st.Charge, Runtime: r.st.Runtime})
			}
			r.lastStatus = &status
		}
		if status.OnBattery() {
			if r.onBatterySince.IsZero() {
				r.onBatterySince = now
			}
		} else {
			r.onBatterySince = time.Time{}
			// mains is back: a user's cancel and a dry run's disarm end
			if r.st.Suppressed {
				r.st.Suppressed = false
			}
			r.armed = true
		}
	}
	if !r.lastOK.IsZero() {
		ms := r.lastOK.UnixMilli()
		r.st.LastOK = &ms
		age := int(now.Sub(r.lastOK).Seconds())
		r.st.DataAgeS = &age
	}
	r.st.OnBattery = !r.onBatterySince.IsZero()
	if r.st.OnBattery {
		r.st.OnBatteryS = int(now.Sub(r.onBatterySince).Seconds())
	} else {
		r.st.OnBatteryS = -1
	}
	r.st.Reasons = m.reasons(st, u, r, now)
}

func (m *Monitor) readVars(ctx context.Context, sv config.Server, name string) (map[string]string, error) {
	c, err := m.conn(ctx, sv)
	if err != nil {
		return nil, err
	}
	vars, err := c.ListVar(ctx, name)
	if err != nil {
		// a protocol error (stale, unknown ups) keeps the connection; anything else drops it
		var ne *nut.Error
		if !errors.As(err, &ne) {
			m.dropConn(sv.ID)
			// one retry on a fresh connection: upsd may have closed an idle one
			c, derr := m.conn(ctx, sv)
			if derr != nil {
				return nil, derr
			}
			vars, err = c.ListVar(ctx, name)
			if err != nil {
				m.dropConn(sv.ID)
				return nil, err
			}
			return vars, nil
		}
		return nil, err
	}
	return vars, nil
}

// reasons applies the UPS's rules to its state.
func (m *Monitor) reasons(st config.Settings, u config.UPS, r *upsRun, now time.Time) []Reason {
	out := []Reason{}
	if !u.Enabled {
		return out
	}
	rules := u.Rules
	if !r.st.Connected {
		// communication lost while the UPS was on battery (upsmon's rule)
		if rules.CommLost && r.lastStatus != nil && r.lastStatus.OnBattery() && !r.lastOK.IsZero() {
			if age := now.Sub(r.lastOK); age >= time.Duration(st.DeadS)*time.Second {
				out = append(out, Reason{Rule: ReasonCommLost, Value: age.Seconds(), Threshold: float64(st.DeadS)})
			}
		}
		return out
	}
	status := nut.ParseStatus(r.st.Vars["ups.status"])
	if rules.FSD && status.Has(nut.FlagFSD) {
		out = append(out, Reason{Rule: ReasonFSD})
	}
	if !status.OnBattery() {
		return out
	}
	if rules.LowBattery && status.Has(nut.FlagLowBattery) {
		out = append(out, Reason{Rule: ReasonLowBattery})
	}
	if rules.ChargeBelow > 0 && r.st.Charge != nil && *r.st.Charge < float64(rules.ChargeBelow) {
		out = append(out, Reason{Rule: ReasonCharge, Value: *r.st.Charge, Threshold: float64(rules.ChargeBelow)})
	}
	if rules.RuntimeBelow > 0 && r.st.Runtime != nil && *r.st.Runtime < float64(rules.RuntimeBelow) {
		out = append(out, Reason{Rule: ReasonRuntime, Value: *r.st.Runtime, Threshold: float64(rules.RuntimeBelow)})
	}
	if rules.OnBatteryLonger > 0 && !r.onBatterySince.IsZero() {
		if d := now.Sub(r.onBatterySince); d >= time.Duration(rules.OnBatteryLonger)*time.Second {
			out = append(out, Reason{Rule: ReasonDuration, Value: d.Seconds(), Threshold: float64(rules.OnBatteryLonger)})
		}
	}
	return out
}

// decide starts, cancels or finishes the countdown.
func (m *Monitor) decide(ctx context.Context, c config.Config, now time.Time) {
	m.mu.Lock()
	st := c.Settings
	var reasons []string
	critical, enabled, armed := 0, 0, 0
	fsd := false
	ids := make([]string, 0, len(m.ups))
	for id := range m.ups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := m.ups[id]
		if !r.st.Enabled {
			continue
		}
		enabled++
		if len(r.st.Reasons) == 0 || r.st.Suppressed {
			continue
		}
		critical++
		if r.armed {
			armed++
		}
		for _, rs := range r.st.Reasons {
			reasons = append(reasons, r.st.Label+": "+rs.Rule)
			if rs.Rule == ReasonFSD {
				fsd = true
			}
		}
	}
	trigger := st.Enabled && enabled > 0 && critical > 0 && armed > 0
	if st.RequireAll && critical < enabled {
		trigger = false
	}
	if m.issued {
		m.mu.Unlock()
		return
	}
	switch {
	case trigger && m.cd == nil:
		if !now.Before(m.retryAt) {
			grace := time.Duration(st.GraceS) * time.Second
			if fsd {
				grace = 0
			}
			m.cd = &countdown{since: now, until: now.Add(grace), reasons: reasons, dryRun: st.DryRun, action: st.Action, fsd: fsd}
			m.o.Events.Add(Event{At: now, Kind: KindCountdown, Reasons: reasons, GraceS: int(grace.Seconds()), DryRun: st.DryRun, Action: st.Action})
			m.o.Log("countdown: %s in %s (%s)", st.Action, grace, strings.Join(reasons, ", "))
		}
	case !trigger && m.cd != nil:
		m.o.Events.Add(Event{At: now, Kind: KindCancelled, Reasons: m.cd.reasons, Message: "condition cleared"})
		m.o.Log("countdown cancelled: the condition cleared")
		m.cd = nil
	case trigger && m.cd != nil:
		// a new FSD shortens a running countdown to now
		if fsd && !m.cd.fsd {
			m.cd.fsd = true
			m.cd.until = now
		}
		m.cd.reasons = reasons
	}
	if m.cd == nil || now.Before(m.cd.until) {
		m.mu.Unlock()
		return
	}
	cd := *m.cd
	m.cd = nil
	m.mu.Unlock()
	m.execute(ctx, cd.action, cd.dryRun, false, cd.reasons, now)
}

// execute runs the shutdown (or the dry run) and records it.
func (m *Monitor) execute(ctx context.Context, action string, dryRun, test bool, reasons []string, now time.Time) *Shutdown {
	rec := &Shutdown{At: now.UnixMilli(), Action: action, DryRun: dryRun, Test: test, Reasons: reasons}
	if dryRun {
		m.o.Log("dry run: would %s the system (%s)", action, strings.Join(reasons, ", "))
		m.o.Events.Add(Event{At: now, Kind: KindDryRun, Action: action, Reasons: reasons, Test: test})
	} else {
		m.o.Log("shutting the system down: %s (%s)", action, strings.Join(reasons, ", "))
		if err := m.o.Executor.Shutdown(ctx, action); err != nil {
			rec.Error = err.Error()
			m.o.Log("shutdown failed: %v", err)
			m.o.Events.Add(Event{At: now, Kind: KindError, Action: action, Reasons: reasons, Message: err.Error(), Test: test})
		} else {
			m.o.Events.Add(Event{At: now, Kind: KindShutdown, Action: action, Reasons: reasons, Test: test})
		}
	}
	m.mu.Lock()
	m.last = rec
	if !test {
		if rec.Error != "" {
			// try again in a while, while the condition holds
			m.retryAt = now.Add(30 * time.Second)
		} else if dryRun {
			for _, r := range m.ups {
				if len(r.st.Reasons) > 0 {
					r.armed = false
				}
			}
		} else {
			m.issued = true
		}
	} else if !dryRun && rec.Error == "" {
		m.issued = true
	}
	m.mu.Unlock()
	if m.o.OnChange != nil {
		m.o.OnChange()
	}
	return rec
}

// Test runs the shutdown now, as a dry run or for real (the page's Test button).
func (m *Monitor) Test(ctx context.Context, real bool) *Shutdown {
	c := m.o.Store.Get()
	return m.execute(ctx, c.Settings.Action, !real, true, []string{"test"}, m.o.Now())
}

// Cancel ends a running countdown; the UPS that caused it stay suppressed until mains returns.
func (m *Monitor) Cancel() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cd == nil {
		return false
	}
	for _, r := range m.ups {
		if len(r.st.Reasons) > 0 {
			r.st.Suppressed = true
		}
	}
	m.o.Events.Add(Event{At: m.o.Now(), Kind: KindCancelled, Reasons: m.cd.reasons, Message: "cancelled by the user"})
	m.cd = nil
	return true
}

// State is the page's status.
func (m *Monitor) State() State {
	c := m.o.Store.Get()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := State{Enabled: c.Settings.Enabled, DryRun: c.Settings.DryRun, Action: c.Settings.Action, RequireAll: c.Settings.RequireAll, GraceS: c.Settings.GraceS, UPS: []UPSState{}, Issued: m.issued, Polls: m.polls, Ready: m.ready}
	if !m.lastPoll.IsZero() {
		ms := m.lastPoll.UnixMilli()
		s.LastPoll = &ms
	}
	for _, u := range c.UPS {
		if r := m.ups[u.ID]; r != nil {
			st := r.st
			if st.Reasons == nil {
				st.Reasons = []Reason{}
			}
			if st.Status == nil {
				st.Status = []string{}
			}
			s.UPS = append(s.UPS, st)
		} else {
			s.UPS = append(s.UPS, UPSState{ID: u.ID, Label: u.Label, Server: u.Server, Name: u.Name, Enabled: u.Enabled, Status: []string{}, Reasons: []Reason{}, OnBatteryS: -1, Rules: u.Rules})
		}
	}
	if m.cd != nil {
		s.Countdown = &Countdown{Since: m.cd.since.UnixMilli(), Until: m.cd.until.UnixMilli(), Reasons: m.cd.reasons, DryRun: m.cd.dryRun, Action: m.cd.action}
	}
	s.Shutdown = m.last
	return s
}

func floatPtr(vars map[string]string, name string) *float64 {
	if f, ok := nut.Float(vars, name); ok {
		return &f
	}
	return nil
}

func first(vars map[string]string, names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(vars[n]); v != "" {
			return v
		}
	}
	return ""
}

// Describe is a one-line summary of a reason for the journal.
func (r Reason) Describe() string {
	switch r.Rule {
	case ReasonCharge:
		return fmt.Sprintf("charge %.0f %% < %.0f %%", r.Value, r.Threshold)
	case ReasonRuntime:
		return fmt.Sprintf("runtime %.0f s < %.0f s", r.Value, r.Threshold)
	case ReasonDuration:
		return fmt.Sprintf("on battery %.0f s >= %.0f s", r.Value, r.Threshold)
	case ReasonCommLost:
		return fmt.Sprintf("no data for %.0f s", r.Value)
	}
	return r.Rule
}

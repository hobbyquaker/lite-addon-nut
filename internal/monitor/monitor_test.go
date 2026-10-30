package monitor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/lite-addon-nut/internal/config"
	"github.com/hobbyquaker/lite-addon-nut/internal/nut"
)

type fakeExec struct {
	mu      sync.Mutex
	calls   []string
	fail    error
	failFor int
}

func (f *fakeExec) Shutdown(_ context.Context, action string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, action)
	if f.fail != nil && (f.failFor == 0 || len(f.calls) <= f.failFor) {
		return f.fail
	}
	return nil
}

func (f *fakeExec) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type world struct {
	t     *testing.T
	ups   *nut.FakeUPSD
	store *config.Store
	exec  *fakeExec
	clk   *clock
	m     *Monitor
	log   *Log
}

func setup(t *testing.T, settings config.Settings, rules config.Rules) *world {
	t.Helper()
	f, err := nut.NewFakeUPSD("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	f.AddUPS("eaton", "Eaton", nut.DemoVars("OL CHRG", 100, 1800))
	f.AddUser("mon", "pw")
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutServer(config.Server{ID: "srv1", Name: "lab", Host: "127.0.0.1", Port: f.Port(), Username: "mon", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutUPS(config.UPS{ID: "ups1", Server: "srv1", Name: "eaton", Label: "Eaton", Enabled: true, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutSettings(settings); err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	ex := &fakeExec{}
	lg := NewLog("", 100)
	m := New(Options{Store: store, Executor: ex, Events: lg, Now: clk.now, Log: t.Logf})
	t.Cleanup(m.closeAll)
	return &world{t: t, ups: f, store: store, exec: ex, clk: clk, m: m, log: lg}
}

func (w *world) poll() State {
	w.m.Poll(context.Background())
	return w.m.State()
}

func (w *world) kinds() []string {
	var out []string
	for _, e := range w.log.List() {
		out = append(out, e.Kind)
	}
	return out
}

func settings(grace int) config.Settings {
	return config.Settings{Enabled: true, Action: config.ActionPoweroff, GraceS: grace, PollS: 1, DeadS: 10}
}

func TestOnlineNothingHappens(t *testing.T) {
	w := setup(t, settings(30), config.DefaultRules)
	s := w.poll()
	if len(s.UPS) != 1 || !s.UPS[0].Connected || s.UPS[0].Status[0] != "OL" || *s.UPS[0].Charge != 100 || s.UPS[0].Model != "Ellipse ECO 650" || s.UPS[0].OnBatteryS != -1 {
		t.Fatalf("%+v", s.UPS[0])
	}
	if s.Countdown != nil || w.exec.count() != 0 || !s.Ready {
		t.Error("nothing should happen on mains")
	}
	// on battery without a rule firing: no countdown
	w.ups.Set("eaton", "ups.status", "OB DISCHRG")
	w.clk.advance(time.Second)
	s = w.poll()
	if !s.UPS[0].OnBattery || s.Countdown != nil {
		t.Errorf("OB without LB: %+v", s)
	}
	if k := w.kinds(); len(k) != 2 || k[0] != KindConnected || k[1] != KindStatus {
		t.Errorf("events %v", k)
	}
}

func TestLowBatteryCountdownAndShutdown(t *testing.T) {
	w := setup(t, settings(30), config.DefaultRules)
	w.poll()
	w.ups.Set("eaton", "ups.status", "OB DISCHRG LB")
	w.ups.Set("eaton", "battery.charge", "8")
	w.clk.advance(time.Second)
	s := w.poll()
	if s.Countdown == nil || s.Countdown.Until-s.Countdown.Since != 30000 || s.Countdown.Action != "poweroff" || s.UPS[0].Reasons[0].Rule != ReasonLowBattery {
		t.Fatalf("countdown: %+v", s)
	}
	w.clk.advance(10 * time.Second)
	if s = w.poll(); w.exec.count() != 0 || s.Countdown == nil {
		t.Fatal("too early")
	}
	w.clk.advance(21 * time.Second)
	s = w.poll()
	if w.exec.count() != 1 || w.exec.calls[0] != "poweroff" || s.Countdown != nil || s.Shutdown == nil || s.Shutdown.DryRun || !s.Issued {
		t.Fatalf("shutdown: %d %+v", w.exec.count(), s)
	}
	// nothing more happens after the order
	w.clk.advance(time.Minute)
	w.poll()
	if w.exec.count() != 1 {
		t.Error("ordered twice")
	}
	if k := w.kinds(); k[len(k)-1] != KindShutdown || k[len(k)-2] != KindCountdown {
		t.Errorf("events %v", k)
	}
}

func TestMainsReturnsCancels(t *testing.T) {
	w := setup(t, settings(60), config.Rules{ChargeBelow: 50, LowBattery: true, FSD: true})
	w.poll()
	w.ups.Set("eaton", "ups.status", "OB DISCHRG")
	w.ups.Set("eaton", "battery.charge", "40")
	w.clk.advance(time.Second)
	if s := w.poll(); s.Countdown == nil || s.UPS[0].Reasons[0].Rule != ReasonCharge || s.UPS[0].Reasons[0].Threshold != 50 {
		t.Fatalf("%+v", s)
	}
	w.ups.Set("eaton", "ups.status", "OL CHRG")
	w.clk.advance(10 * time.Second)
	if s := w.poll(); s.Countdown != nil {
		t.Fatal("mains is back: the countdown must be cancelled")
	}
	w.clk.advance(2 * time.Minute)
	if w.poll(); w.exec.count() != 0 {
		t.Error("shut down after the cancel")
	}
	if k := w.kinds(); k[len(k)-1] != KindCancelled {
		t.Errorf("events %v", k)
	}
}

func TestFSDIsImmediate(t *testing.T) {
	w := setup(t, settings(300), config.DefaultRules)
	w.poll()
	w.ups.Set("eaton", "ups.status", "OB DISCHRG FSD")
	w.clk.advance(time.Second)
	w.poll()
	if w.exec.count() != 1 {
		t.Fatal("FSD must shut down without a grace period")
	}
}

func TestRuntimeAndDuration(t *testing.T) {
	w := setup(t, settings(0), config.Rules{RuntimeBelow: 300, OnBatteryLonger: 120})
	w.poll()
	w.ups.Set("eaton", "ups.status", "OB DISCHRG")
	w.ups.Set("eaton", "battery.runtime", "900")
	w.clk.advance(time.Second)
	if s := w.poll(); len(s.UPS[0].Reasons) != 0 || s.UPS[0].OnBatteryS != 0 {
		t.Fatalf("%+v", s.UPS[0])
	}
	w.clk.advance(60 * time.Second)
	if s := w.poll(); len(s.UPS[0].Reasons) != 0 || s.UPS[0].OnBatteryS != 60 {
		t.Fatalf("after 60 s: %+v", s.UPS[0].Reasons)
	}
	w.ups.Set("eaton", "battery.runtime", "200")
	w.clk.advance(time.Second)
	s := w.poll()
	if len(s.UPS[0].Reasons) != 1 || s.UPS[0].Reasons[0].Rule != ReasonRuntime || w.exec.count() != 1 {
		t.Fatalf("runtime: %+v calls %d", s.UPS[0].Reasons, w.exec.count())
	}
}

func TestDurationRule(t *testing.T) {
	w := setup(t, settings(0), config.Rules{OnBatteryLonger: 120})
	w.poll()
	w.ups.Set("eaton", "ups.status", "OB DISCHRG")
	w.clk.advance(time.Second)
	w.poll()
	w.clk.advance(119 * time.Second)
	if w.poll(); w.exec.count() != 0 {
		t.Fatal("too early")
	}
	w.clk.advance(2 * time.Second)
	if s := w.poll(); w.exec.count() != 1 || s.UPS[0].Reasons[0].Rule != ReasonDuration {
		t.Fatalf("%+v", s.UPS[0].Reasons)
	}
}

func TestDryRunDisarmsUntilMains(t *testing.T) {
	st := settings(0)
	st.DryRun = true
	w := setup(t, st, config.DefaultRules)
	w.poll()
	w.ups.Set("eaton", "ups.status", "OB LB")
	w.clk.advance(time.Second)
	s := w.poll()
	if w.exec.count() != 0 || s.Shutdown == nil || !s.Shutdown.DryRun || s.Issued {
		t.Fatalf("dry run: %+v", s.Shutdown)
	}
	w.clk.advance(5 * time.Second)
	w.poll()
	if n := len(w.log.List()); n != 4 { // connected, status, countdown, dry_run
		t.Errorf("a dry run must not repeat: %v", w.kinds())
	}
	// mains back, then battery again: a second dry run
	w.ups.Set("eaton", "ups.status", "OL")
	w.clk.advance(time.Second)
	w.poll()
	w.ups.Set("eaton", "ups.status", "OB LB")
	w.clk.advance(time.Second)
	w.poll()
	if k := w.kinds(); k[len(k)-1] != KindDryRun {
		t.Errorf("events %v", k)
	}
}

func TestUserCancelSuppressesUntilMains(t *testing.T) {
	w := setup(t, settings(60), config.DefaultRules)
	w.poll()
	w.ups.Set("eaton", "ups.status", "OB LB")
	w.clk.advance(time.Second)
	if s := w.poll(); s.Countdown == nil {
		t.Fatal("no countdown")
	}
	if !w.m.Cancel() {
		t.Fatal("cancel")
	}
	w.clk.advance(2 * time.Minute)
	if s := w.poll(); s.Countdown != nil || !s.UPS[0].Suppressed || w.exec.count() != 0 {
		t.Fatalf("suppressed: %+v", s)
	}
	w.ups.Set("eaton", "ups.status", "OL")
	w.clk.advance(time.Second)
	if s := w.poll(); s.UPS[0].Suppressed {
		t.Fatal("mains ends the suppression")
	}
	w.ups.Set("eaton", "ups.status", "OB LB")
	w.clk.advance(time.Second)
	if s := w.poll(); s.Countdown == nil {
		t.Fatal("armed again")
	}
}

func TestCommLostOnBattery(t *testing.T) {
	w := setup(t, settings(0), config.DefaultRules)
	w.poll()
	w.ups.Set("eaton", "ups.status", "OB DISCHRG")
	w.clk.advance(time.Second)
	w.poll()
	w.ups.SetStale("eaton", true)
	w.clk.advance(time.Second)
	s := w.poll()
	if s.UPS[0].Connected || !nut.IsCode(errors.New(s.UPS[0].Error), "") && s.UPS[0].Error == "" {
		t.Fatalf("stale: %+v", s.UPS[0])
	}
	if w.exec.count() != 0 {
		t.Fatal("too early for comm_lost")
	}
	w.clk.advance(10 * time.Second)
	s = w.poll()
	if len(s.UPS[0].Reasons) != 1 || s.UPS[0].Reasons[0].Rule != ReasonCommLost || w.exec.count() != 1 {
		t.Fatalf("comm lost: %+v calls %d", s.UPS[0].Reasons, w.exec.count())
	}
	if k := w.kinds(); k[2] != KindCommLost {
		t.Errorf("events %v", k)
	}
}

func TestCommLostOnMainsIsNoReason(t *testing.T) {
	w := setup(t, settings(0), config.DefaultRules)
	w.poll()
	w.ups.Close()
	w.clk.advance(time.Minute)
	s := w.poll()
	if s.UPS[0].Connected || len(s.UPS[0].Reasons) != 0 || w.exec.count() != 0 {
		t.Fatalf("%+v", s.UPS[0])
	}
}

func TestServerDownBeforeAnyContact(t *testing.T) {
	w := setup(t, settings(0), config.DefaultRules)
	w.ups.Close()
	s := w.poll()
	if s.UPS[0].Connected || s.UPS[0].Error == "" || s.UPS[0].LastOK != nil || len(s.UPS[0].Reasons) != 0 {
		t.Fatalf("%+v", s.UPS[0])
	}
}

func TestRequireAll(t *testing.T) {
	st := settings(0)
	st.RequireAll = true
	w := setup(t, st, config.DefaultRules)
	w.ups.AddUPS("apc", "APC", nut.DemoVars("OL", 100, 1000))
	if _, err := w.store.PutUPS(config.UPS{ID: "ups2", Server: "srv1", Name: "apc", Enabled: true, Rules: config.DefaultRules}); err != nil {
		t.Fatal(err)
	}
	w.poll()
	w.ups.Set("eaton", "ups.status", "OB LB")
	w.clk.advance(time.Second)
	if w.poll(); w.exec.count() != 0 {
		t.Fatal("one of two is not all")
	}
	w.ups.Set("apc", "ups.status", "OB LB")
	w.clk.advance(time.Second)
	if w.poll(); w.exec.count() != 1 {
		t.Fatal("both critical: shut down")
	}
}

func TestDisabledUPSAndWatch(t *testing.T) {
	st := settings(0)
	st.Enabled = false
	w := setup(t, st, config.DefaultRules)
	w.poll()
	w.ups.Set("eaton", "ups.status", "OB LB")
	w.clk.advance(time.Second)
	if s := w.poll(); w.exec.count() != 0 || len(s.UPS[0].Reasons) != 1 {
		t.Fatal("the watch is off: reasons shown, nothing done")
	}
	st.Enabled = true
	if _, err := w.store.PutSettings(st); err != nil {
		t.Fatal(err)
	}
	c := w.store.Get()
	c.UPS[0].Enabled = false
	if _, err := w.store.PutUPS(c.UPS[0]); err != nil {
		t.Fatal(err)
	}
	w.clk.advance(time.Second)
	if s := w.poll(); w.exec.count() != 0 || len(s.UPS[0].Reasons) != 0 {
		t.Fatal("a disabled UPS has no reasons")
	}
}

func TestExecutorFailureRetries(t *testing.T) {
	w := setup(t, settings(0), config.DefaultRules)
	w.exec.fail = errors.New("403 forbidden")
	w.exec.failFor = 1
	w.poll()
	w.ups.Set("eaton", "ups.status", "OB LB")
	w.clk.advance(time.Second)
	s := w.poll()
	if w.exec.count() != 1 || s.Shutdown == nil || s.Shutdown.Error == "" || s.Issued {
		t.Fatalf("%+v", s.Shutdown)
	}
	w.clk.advance(5 * time.Second)
	if w.poll(); w.exec.count() != 1 {
		t.Fatal("retried too soon")
	}
	w.clk.advance(30 * time.Second)
	if s := w.poll(); w.exec.count() != 2 || !s.Issued {
		t.Fatalf("retry: %d %+v", w.exec.count(), s.Shutdown)
	}
}

func TestTestButton(t *testing.T) {
	w := setup(t, settings(0), config.DefaultRules)
	rec := w.m.Test(context.Background(), false)
	if !rec.DryRun || !rec.Test || w.exec.count() != 0 || w.m.State().Issued {
		t.Fatalf("%+v", rec)
	}
	rec = w.m.Test(context.Background(), true)
	if rec.DryRun || w.exec.count() != 1 || !w.m.State().Issued {
		t.Fatalf("%+v", rec)
	}
}

func TestLogFile(t *testing.T) {
	path := t.TempDir() + "/events.json"
	l := NewLog(path, 3)
	for i := 0; i < 5; i++ {
		l.Add(Event{At: time.Unix(int64(i), 0), Kind: KindStatus})
	}
	l2 := NewLog(path, 3)
	ev := l2.List()
	if len(ev) != 3 || ev[0].At.Unix() != 2 {
		t.Errorf("%+v", ev)
	}
	l2.Clear()
	if len(NewLog(path, 3).List()) != 0 {
		t.Error("clear")
	}
}

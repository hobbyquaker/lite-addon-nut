// Package upsd tests the client and the monitor against a real upsd: Debian's nut-server with the
// dummy-ups driver in docker (test/upsd/Dockerfile), the UPS driven through upsrw. Run with
// NUT_DOCKER=1 (CI does; without it the test is skipped).
package upsd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/lite-addon-nut/internal/config"
	"github.com/hobbyquaker/lite-addon-nut/internal/monitor"
	"github.com/hobbyquaker/lite-addon-nut/internal/nut"
)

const image = "lite-addon-nut-upsd:test"

type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) Shutdown(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, config.ActionPoweroff)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestAgainstRealUpsd(t *testing.T) {
	if os.Getenv("NUT_DOCKER") == "" {
		t.Skip("NUT_DOCKER not set")
	}
	run(t, "docker", "build", "-q", "-t", image, ".")
	name := fmt.Sprintf("lite-addon-nut-upsd-%d", time.Now().UnixNano())
	run(t, "docker", "run", "-d", "--rm", "--name", name, "-p", "127.0.0.1:0:3493", image)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("container log:\n%s", strings.TrimSpace(string(must(exec.Command("docker", "logs", name).CombinedOutput()))))
		}
		_ = exec.Command("docker", "stop", "-t", "2", name).Run()
	})
	portLine := run(t, "docker", "port", name, "3493/tcp")
	var port int
	for _, l := range strings.Split(portLine, "\n") {
		if i := strings.LastIndex(l, ":"); i >= 0 {
			fmt.Sscanf(l[i+1:], "%d", &port)
		}
	}
	if port == 0 {
		t.Fatalf("no port: %q", portLine)
	}
	ctx := context.Background()
	o := nut.Options{Host: "127.0.0.1", Port: port, Timeout: 3 * time.Second}

	// wait for upsd and its driver
	var res *nut.ProbeResult
	var err error
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		res, err = nut.Probe(ctx, o)
		if err == nil && len(res.UPS) == 1 {
			// the driver answers ups.status WAIT until it has read its file
			c, derr := nut.Dial(ctx, o)
			if derr == nil {
				vars, verr := c.ListVar(ctx, "dummy")
				c.Close()
				if verr == nil && vars["ups.status"] != "WAIT" && vars["battery.charge"] != "" {
					break
				}
				err = verr
				if err == nil {
					err = fmt.Errorf("driver not ready: %v", vars)
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("upsd did not come up: %v", err)
	}
	t.Logf("upsd: %s, UPS %v", res.Version, res.UPS)
	if !strings.Contains(res.Version, "Network UPS Tools") || res.UPS[0].Name != "dummy" || res.UPS[0].Description != "Test UPS (dummy-ups)" {
		t.Errorf("probe: %+v", res)
	}

	t.Run("vars", func(t *testing.T) {
		c, err := nut.Dial(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		vars, err := c.ListVar(ctx, "dummy")
		if err != nil {
			t.Fatal(err)
		}
		if vars["ups.status"] != "OL CHRG" || vars["battery.charge"] != "100" || vars["ups.model"] != "Dummy 1000" || vars["driver.name"] != "dummy-ups" {
			t.Errorf("vars %v", vars)
		}
		if v, err := c.GetVar(ctx, "dummy", "ups.load"); err != nil || v != "25" {
			t.Errorf("GetVar %q %v", v, err)
		}
		if _, err := c.ListVar(ctx, "nope"); !nut.IsCode(err, "UNKNOWN-UPS") {
			t.Errorf("unknown ups: %v", err)
		}
	})

	t.Run("login", func(t *testing.T) {
		lo := o
		lo.Username, lo.Password = "monuser", "monpass"
		if r, err := nut.Probe(ctx, lo); err != nil || len(r.UPS) != 1 {
			t.Errorf("login: %v", err)
		}
		// upsd checks the password only at an action: a wrong one is accepted on a read
		lo.Password = "wrong"
		if _, err := nut.Probe(ctx, lo); err != nil {
			t.Logf("this upsd refuses a wrong password on PASSWORD: %v", err)
		}
	})

	t.Run("starttls", func(t *testing.T) {
		to := o
		to.TLS = nut.TLSStartTLS
		to.InsecureSkipVerify = true
		r, err := nut.Probe(ctx, to)
		if nut.IsCode(err, "FEATURE-NOT-CONFIGURED") || nut.IsCode(err, "FEATURE-NOT-SUPPORTED") {
			t.Skipf("this upsd has no SSL: %v", err)
		}
		if err != nil || !r.TLS {
			t.Fatalf("starttls: %v %+v", err, r)
		}
		c, err := nut.Dial(ctx, to)
		if err != nil {
			t.Fatal(err)
		}
		vars, err := c.ListVar(ctx, "dummy")
		c.Close()
		if err != nil || vars["ups.status"] == "" {
			t.Errorf("over TLS: %v", err)
		}
		// the pinned certificate
		to.InsecureSkipVerify = false
		to.CACert = run(t, "docker", "exec", name, "cat", "/etc/nut/ca.pem")
		if r, err := nut.Probe(ctx, to); err != nil || !r.TLS {
			t.Errorf("pinned: %v", err)
		}
		to.CACert = ""
		if _, err := nut.Probe(ctx, to); err == nil {
			t.Error("the self-signed certificate must be refused without the CA")
		}
	})

	t.Run("monitor", func(t *testing.T) {
		// upsrw takes one -s per call
		set := func(kv ...string) {
			for _, s := range kv {
				run(t, "docker", "exec", name, "upsrw", "-s", s, "-u", "admin", "-p", "secret", "dummy@localhost")
			}
		}
		store, err := config.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutServer(config.Server{ID: "srv1", Name: "docker", Host: "127.0.0.1", Port: port}); err != nil {
			t.Fatal(err)
		}
		rules := config.DefaultRules
		rules.ChargeBelow = 30
		if _, err := store.PutUPS(config.UPS{ID: "ups1", Server: "srv1", Name: "dummy", Label: "Dummy", Enabled: true, Rules: rules}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutSettings(config.Settings{Enabled: true, GraceS: 3, PollS: 1, DeadS: 5}); err != nil {
			t.Fatal(err)
		}
		rec := &recorder{}
		lg := monitor.NewLog("", 100)
		m := monitor.New(monitor.Options{Store: store, Executor: rec, Events: lg, Log: t.Logf})
		mctx, cancel := context.WithCancel(ctx)
		defer cancel()
		go m.Run(mctx)

		wait := func(what string, cond func(s monitor.State) bool) monitor.State {
			t.Helper()
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				s := m.State()
				if cond(s) {
					return s
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatalf("%s did not happen: %+v", what, m.State())
			return monitor.State{}
		}
		s := wait("first poll", func(s monitor.State) bool { return s.Ready && len(s.UPS) == 1 && s.UPS[0].Connected })
		if s.UPS[0].Status[0] != "OL" || *s.UPS[0].Charge != 100 || s.UPS[0].Model != "Dummy 1000" {
			t.Fatalf("%+v", s.UPS[0])
		}

		// mains fails: on battery, charge above the threshold, nothing happens
		set("ups.status=OB DISCHRG", "battery.charge=60", "battery.runtime=900")
		s = wait("on battery", func(s monitor.State) bool { return s.UPS[0].OnBattery })
		if len(s.UPS[0].Reasons) != 0 || s.Countdown != nil {
			t.Fatalf("too early: %+v", s.UPS[0])
		}

		// the charge falls below 30 %: the countdown starts (3 s) and the shutdown is ordered
		set("battery.charge=25", "battery.runtime=300")
		s = wait("countdown", func(s monitor.State) bool { return s.Countdown != nil || rec.count() > 0 })
		wait("shutdown", func(s monitor.State) bool { return rec.count() == 1 })
		s = m.State()
		if !s.Issued || s.Shutdown == nil || s.Shutdown.Action != "poweroff" || len(s.Shutdown.Reasons) != 1 || !strings.Contains(s.Shutdown.Reasons[0], "charge_below") {
			t.Fatalf("shutdown: %+v", s.Shutdown)
		}
		kinds := []string{}
		for _, e := range lg.List() {
			kinds = append(kinds, e.Kind)
		}
		t.Logf("events: %v", kinds)
		if kinds[len(kinds)-1] != monitor.KindShutdown || kinds[len(kinds)-2] != monitor.KindCountdown {
			t.Errorf("events %v", kinds)
		}
		set("ups.status=OL CHRG", "battery.charge=26")
	})
}

func must(b []byte, _ error) []byte { return b }

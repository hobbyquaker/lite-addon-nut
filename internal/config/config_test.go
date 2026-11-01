package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStore(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c := s.Get(); !c.Settings.Enabled || c.Settings.GraceS != 30 || len(c.Servers) != 0 {
		t.Errorf("defaults: %+v", c.Settings)
	}
	changes := 0
	s.OnChange = func() { changes++ }

	c, err := s.PutServer(Server{Name: "nas", Host: "nas.lan", Username: "mon", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Servers) != 1 || c.Servers[0].ID != "srv1" || c.Servers[0].Port != 3493 || c.Servers[0].TLS != "off" || c.Servers[0].Password != "" {
		t.Errorf("server: %+v", c.Servers)
	}
	if sv, ok := s.Server("srv1"); !ok || sv.Password != "secret" {
		t.Error("the store keeps the password")
	}
	// an empty password on an update keeps the old one
	if _, err := s.PutServer(Server{ID: "srv1", Name: "nas2", Host: "nas.lan", Username: "mon"}); err != nil {
		t.Fatal(err)
	}
	if sv, _ := s.Server("srv1"); sv.Password != "secret" || sv.Name != "nas2" {
		t.Errorf("update: %+v", sv)
	}
	if _, err := s.PutServer(Server{Host: "x", Username: "u"}); err == nil {
		t.Error("a user without a password must be refused")
	}
	if _, err := s.PutServer(Server{Host: ""}); err == nil {
		t.Error("an empty host must be refused")
	}
	if _, err := s.PutServer(Server{Host: "x", TLS: "ssl"}); err == nil {
		t.Error("tls ssl must be refused")
	}
	var ve *ValidationError
	if _, err := s.PutServer(Server{Host: "x", Port: 70000}); !errors.As(err, &ve) {
		t.Errorf("port: %v", err)
	}

	c, err = s.PutUPS(UPS{Server: "srv1", Name: "eaton", Enabled: true, Rules: DefaultRules})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.UPS) != 1 || c.UPS[0].ID != "ups1" || c.UPS[0].Label != "eaton" {
		t.Errorf("ups: %+v", c.UPS)
	}
	if _, err := s.PutUPS(UPS{Server: "nope", Name: "x"}); err == nil {
		t.Error("an unknown server must be refused")
	}
	if _, err := s.PutUPS(UPS{Server: "srv1", Name: "x", Rules: Rules{ChargeBelow: 101}}); err == nil {
		t.Error("a charge above 100 must be refused")
	}
	if _, err := s.PutSettings(Settings{Enabled: true, DryRun: true, GraceS: 10, PollS: 2, DeadS: 30}); err != nil {
		t.Fatal(err)
	}

	// the file: 0600, re-read
	fi, err := os.Stat(filepath.Join(dir, "nut.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file: %v %v", fi, err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c2 := s2.Get()
	if len(c2.Servers) != 1 || c2.Servers[0].Password != "secret" || len(c2.UPS) != 1 || !c2.Settings.DryRun {
		t.Errorf("re-read: %+v", c2)
	}
	if s2.Public().Servers[0].Password != "" {
		t.Error("Public leaks the password")
	}

	// deleting the server takes its UPS
	c, err = s.DeleteServer("srv1")
	if err != nil || len(c.Servers) != 0 || len(c.UPS) != 0 {
		t.Errorf("delete: %v %+v", err, c)
	}
	if _, err := s.DeleteUPS("ups1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete unknown: %v", err)
	}
	if changes < 5 {
		t.Errorf("OnChange called %d times", changes)
	}
}

// A file of 0.1.x: its action goes for good, and only a reboot is reported, once.
func TestMigrateAction(t *testing.T) {
	for _, tc := range []struct {
		action string
		report bool
	}{{"reboot", true}, {"poweroff", false}} {
		dir := t.TempDir()
		path := filepath.Join(dir, "nut.json")
		old := `{"servers":[],"ups":[],"settings":{"enabled":true,"action":"` + tc.action + `","dry_run":true,"grace_s":45,"poll_s":7,"dead_s":61}}`
		if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Open(dir)
		if err != nil {
			t.Fatal(tc.action, err)
		}
		if s.RebootRemoved() != tc.report {
			t.Errorf("%s: reported %v", tc.action, s.RebootRemoved())
		}
		if c := s.Get().Settings; !c.DryRun || c.GraceS != 45 || c.PollS != 7 || c.DeadS != 61 {
			t.Errorf("%s: the other settings: %+v", tc.action, c)
		}
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), "action") {
			t.Errorf("%s: the key stayed: %s", tc.action, b)
		}
		s2, err := Open(dir)
		if err != nil || s2.RebootRemoved() {
			t.Errorf("%s: the second start: %v %v", tc.action, s2.RebootRemoved(), err)
		}
	}
}

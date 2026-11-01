// Package addonpkg tests the package files the addon ships.
package addonpkg

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit/fragment"
	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit/manifest"
)

// The manifest as the system validates it at install, through the kit's strict load.
func TestManifest(t *testing.T) {
	m, err := manifest.Load("../../addon/openccu-lite.json")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "nut" || m.Undeclared() || m.Runtime == nil || !m.Runtime.Daemon {
		t.Fatalf("manifest: %+v", m)
	}
	if len(m.Runtime.Capabilities) != 0 || len(m.Runtime.Ports) != 0 {
		t.Errorf("the addon needs no capability and no port: %+v", m.Runtime)
	}
	// the one scope: power:halt, for the shutdown and nothing else (an addon token never gets power)
	if len(m.Runtime.APIScopes) != 1 || m.Runtime.APIScopes[0] != "power:halt" {
		t.Errorf("scopes %v", m.Runtime.APIScopes)
	}
	for _, s := range m.Runtime.APIScopes {
		if s == "power" || s == "*" || s == "auth:admin" || s == "backup" {
			t.Errorf("%s is never granted to an addon", s)
		}
	}
	if m.Description["de"] == "" || m.Description["en"] == "" || m.Runtime.Note["de"] == "" || m.Runtime.Note["en"] == "" {
		t.Error("the description and the note in both languages")
	}
	if !strings.Contains(m.Runtime.Note["en"], "power:halt") {
		t.Error("the note explains the scope")
	}
}

// The manifest's version is the changelog's newest.
func TestVersionInChangelog(t *testing.T) {
	var m struct {
		Version string `json:"version"`
	}
	b, _ := os.ReadFile("../../addon/openccu-lite.json")
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	cl, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cl), "## ["+m.Version+"]") {
		t.Errorf("CHANGELOG.md has no entry for %s", m.Version)
	}
}

// The manifest's images are tracked at the path the catalogue reads them from.
func TestImagesTracked(t *testing.T) {
	for _, p := range []string{"addon/nut/www/icon.svg", "addon/nut/www/icon-dark.svg", "addon/nut/etc/lighttpd.conf", "addon/nut/www/lighttpd-error-503.html"} {
		if out, err := exec.Command("git", "-C", "../..", "ls-files", "--error-unmatch", "--", p).CombinedOutput(); err != nil {
			t.Errorf("%s not tracked: %v %s", p, err, out)
		}
	}
}

// The proxy rule is what the kit's renderer writes (apart from the comment lines).
func TestFragment(t *testing.T) {
	want, err := fragment.Render(fragment.Fragment{ID: "nut", Port: 8297, ErrorfilePrefix: "/usr/local/addons/nut/www/lighttpd-error-"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile("../../addon/nut/etc/lighttpd.conf")
	strip := func(s string) string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			if !strings.HasPrefix(l, "#") && strings.TrimSpace(l) != "" {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}
	if strip(string(got)) != strip(want) {
		t.Errorf("etc/lighttpd.conf:\n%s\nwant:\n%s", got, want)
	}
}

// The 503 page names the addon, in the tracked copy and the one the build copies.
func TestErrorPage(t *testing.T) {
	for _, p := range []string{"../../addon/static/lighttpd-error-503.html", "../../addon/nut/www/lighttpd-error-503.html"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "<title>USV / UPS</title>") {
			t.Errorf("%s does not name the addon", p)
		}
	}
}

// The rc.d script and the update_script name no other addon and the daemon's flags are right.
func TestScripts(t *testing.T) {
	for _, p := range []string{"../../addon/nut/rc.d", "../../addon/update_script"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "snmp") || strings.Contains(string(b), "forwarder") {
			t.Errorf("%s names another addon", p)
		}
		if !strings.HasPrefix(string(b), "#!/bin/sh\n") {
			t.Errorf("%s is not a sh script", p)
		}
	}
	rc, _ := os.ReadFile("../../addon/nut/rc.d")
	if !strings.Contains(string(rc), "ADDON=nut\n") || !strings.Contains(string(rc), "Config-Url: /addons/$ADDON/") {
		t.Error("rc.d: the addon id or the Config-Url")
	}
}

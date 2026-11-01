// nut is the lite-addon-nut daemon: a Network UPS Tools client that watches one or more UPS on
// upsd servers and shuts the system down cleanly through openccu-lite's API when a rule fires. It
// serves its page and API on the loopback behind lighttpd's proxy of /addons/nut/. openccu-lite
// only; no listening port beyond the loopback.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit"

	"github.com/hobbyquaker/lite-addon-nut/internal/config"
	"github.com/hobbyquaker/lite-addon-nut/internal/httpapi"
	"github.com/hobbyquaker/lite-addon-nut/internal/monitor"
)

// version is set by the build (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	log.SetFlags(0) // the journal adds the time
	addonDir := flag.String("addon-dir", "/usr/local/addons/nut", "the addon's directory (app/, var/)")
	confDir := flag.String("conf-dir", "/usr/local/etc/config/addons/nut", "the configuration's directory (nut.json; mode 0700)")
	id := flag.String("id", "nut", "the addon id (token file, base path)")
	port := flag.Int("port", 8297, "the loopback port lighttpd proxies to")
	base := flag.String("system", "http://127.0.0.1", "openccu-lite's URL")
	basePath := flag.String("base-path", "", "the path the page is served at (/addons/<id> by default)")
	www := flag.String("www", "", "the built page (<addon-dir>/app by default)")
	tokenFile := flag.String("token-file", "", "a token file for the system's API (the addon's own token by default)")
	noSession := flag.Bool("no-session-check", false, "serve the page without the session check (development only)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *basePath == "" {
		*basePath = "/addons/" + *id
	}
	if *www == "" {
		*www = filepath.Join(*addonDir, "app")
	}
	if err := run(*addonDir, *confDir, *id, *port, *base, *basePath, *www, *tokenFile, *noSession); err != nil {
		log.Fatalf("nut: %v", err)
	}
}

func run(addonDir, confDir, id string, port int, base, basePath, www, tokenFile string, noSession bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logf := func(format string, args ...any) { log.Printf("nut: "+format, args...) }

	store, err := config.Open(confDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(addonDir, "var"), 0o755); err != nil {
		return err
	}
	events := monitor.NewLog(filepath.Join(addonDir, "var", "events.json"), 200)

	var token addonkit.TokenSource
	if tokenFile != "" {
		token = &addonkit.FileToken{Path: tokenFile}
	} else {
		token = addonkit.AddonToken(id)
	}
	sys := &monitor.System{Base: base, HTTP: &http.Client{Transport: &addonkit.AuthTransport{Source: token}, Timeout: 30 * time.Second}, Log: logf}
	// wait for the system: at boot its web server answers 503 until occulited is up, and the
	// token may not exist yet; never a reason to give up
	right, err := sys.Await(ctx)
	if err != nil {
		return nil
	}
	if right.OK {
		logf("the system accepts the token (%v)", right.Scopes)
	} else {
		logf("WARNING: the token lacks power:halt (%v): the system cannot be shut down from here", right.Scopes)
	}

	if store.RebootRemoved() {
		logf("WARNING: the configured action was a reboot, which this version no longer has: the shutdown now powers the system off")
		events.Add(monitor.Event{At: time.Now(), Kind: monitor.KindRebootRemoved})
	}

	mon := monitor.New(monitor.Options{Store: store, Executor: sys, Events: events, Log: logf, Terminating: func() bool { return ctx.Err() != nil }})
	store.OnChange = mon.Kick
	go mon.Run(ctx)

	wrap := func(h http.Handler) http.Handler { return h }
	if !noSession {
		c := addonkit.NewChecker(addonkit.SessionOptions{Base: base, Need: addonkit.LevelAdmin, AddonID: id, OnRefused: func(r *http.Request, reason string) {
			logf("refused %s %s: %s", r.Method, r.URL.Path, reason)
		}})
		wrap = c.Handler
	}
	h := httpapi.New(httpapi.Options{BasePath: basePath, WWW: www, Store: store, Monitor: mon, Events: events, Right: sys, Wrap: wrap, Version: version, Started: time.Now()})
	l, err := addonkit.Listen(port, "")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	c := store.Get()
	logf("%s: serving %s on %s; %d servers, %d UPS%s", version, basePath, l.Addr(), len(c.Servers), len(c.UPS), map[bool]string{true: " (dry run)", false: ""}[c.Settings.DryRun])
	if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
		return err
	}
	logf("stopping")
	os.Stdout.Sync()
	return nil
}

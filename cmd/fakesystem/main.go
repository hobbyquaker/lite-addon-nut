// fakesystem runs the fake openccu-lite and a fake upsd for the page's development and the
// Playwright tests (scripts/dev-serve.sh): the system on -addr, upsd on -upsd with one UPS
// "eaton" on mains, its variables driven over /dev/ups?status=…&charge=…, the shutdown calls at
// /dev/calls.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/hobbyquaker/lite-addon-nut/internal/fakesys"
	"github.com/hobbyquaker/lite-addon-nut/internal/nut"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "the fake system's address")
	upsd := flag.String("upsd", "127.0.0.1:13493", "the fake upsd's address")
	token := flag.String("token", "dev", "the token the fake accepts")
	flag.Parse()
	f, err := nut.NewFakeUPSD(*upsd)
	if err != nil {
		log.Fatal(err)
	}
	f.AddUPS("eaton", "Eaton Ellipse ECO 650 (fake)", nut.DemoVars("OL CHRG", 100, 1860))
	f.AddUPS("apc", "APC Back-UPS 700 (fake)", nut.DemoVars("OL", 97, 1200))
	f.AddUser("monuser", "secret")
	s := fakesys.New(*token, "power:halt", "system:read", "led")
	s.UPSD = f
	log.Printf("fake system on %s, fake upsd on %s", *addr, f.Addr())
	srv := &http.Server{Addr: *addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

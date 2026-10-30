package nut

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
)

// FakeUPSD is a small upsd for the tests and the page's development: LIST UPS, LIST VAR, GET VAR,
// VER, USERNAME/PASSWORD, STARTTLS (with a certificate) and LOGOUT. Its variables are set by the
// test and change at once; a UPS marked stale answers DATA-STALE as upsd does when its driver
// stopped updating.
type FakeUPSD struct {
	mu       sync.Mutex
	ups      map[string]*fakeUPS
	users    map[string]string // name → password; empty: everybody is let in
	cert     *tls.Certificate
	ln       net.Listener
	conns    map[net.Conn]bool
	Requests []string
	// RequireLogin refuses LIST and GET without a known login. upsd itself accepts any
	// USERNAME/PASSWORD and refuses nothing read-only; it checks credentials at an action only.
	RequireLogin bool
	// Version is what VER answers.
	Version string
}

type fakeUPS struct {
	desc  string
	vars  map[string]string
	stale bool
}

// NewFakeUPSD listens on addr ("127.0.0.1:0" for a free port).
func NewFakeUPSD(addr string) (*FakeUPSD, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	f := &FakeUPSD{ups: map[string]*fakeUPS{}, users: map[string]string{}, ln: ln, conns: map[net.Conn]bool{}, Version: "Network UPS Tools upsd 2.8.2 (fake)"}
	go f.serve()
	return f, nil
}

// Addr is where it listens.
func (f *FakeUPSD) Addr() string { return f.ln.Addr().String() }

// Port is the port it listens on.
func (f *FakeUPSD) Port() int { return f.ln.Addr().(*net.TCPAddr).Port }

// Close stops it and cuts every open connection (the server went away).
func (f *FakeUPSD) Close() error {
	err := f.ln.Close()
	f.mu.Lock()
	for c := range f.conns {
		c.Close()
		delete(f.conns, c)
	}
	f.mu.Unlock()
	return err
}

// SetCert enables STARTTLS with that certificate.
func (f *FakeUPSD) SetCert(c tls.Certificate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cert = &c
}

// AddUser adds a login.
func (f *FakeUPSD) AddUser(name, password string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[name] = password
}

// AddUPS adds a UPS with its variables.
func (f *FakeUPSD) AddUPS(name, desc string, vars map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := map[string]string{}
	for k, v := range vars {
		cp[k] = v
	}
	f.ups[name] = &fakeUPS{desc: desc, vars: cp}
}

// Set changes one variable.
func (f *FakeUPSD) Set(ups, name, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u := f.ups[ups]; u != nil {
		u.vars[name] = value
	}
}

// SetStale marks a UPS stale (its driver stopped updating) or fresh again.
func (f *FakeUPSD) SetStale(ups string, stale bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u := f.ups[ups]; u != nil {
		u.stale = stale
	}
}

func (f *FakeUPSD) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *FakeUPSD) handle(conn net.Conn) {
	raw := conn
	f.mu.Lock()
	f.conns[raw] = true
	f.mu.Unlock()
	defer func() {
		raw.Close()
		f.mu.Lock()
		delete(f.conns, raw)
		f.mu.Unlock()
	}()
	r := bufio.NewReader(conn)
	var user string
	loggedIn := false
	write := func(s string) { _, _ = conn.Write([]byte(s + "\n")) }
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.mu.Lock()
		f.Requests = append(f.Requests, line)
		f.mu.Unlock()
		fields, err := Fields(line)
		if err != nil || len(fields) == 0 {
			write("ERR INVALID-ARGUMENT")
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "STARTTLS":
			f.mu.Lock()
			cert := f.cert
			f.mu.Unlock()
			if cert == nil {
				write("ERR FEATURE-NOT-CONFIGURED")
				continue
			}
			write("OK STARTTLS")
			tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*cert}, MinVersion: tls.VersionTLS12})
			if err := tc.Handshake(); err != nil {
				return
			}
			conn = tc
			r = bufio.NewReader(tc)
			write = func(s string) { _, _ = conn.Write([]byte(s + "\n")) }
		case "USERNAME":
			if len(fields) < 2 {
				write("ERR INVALID-ARGUMENT")
				continue
			}
			if user != "" {
				write("ERR ALREADY-SET-USERNAME")
				continue
			}
			user = fields[1]
			write("OK")
		case "PASSWORD":
			if len(fields) < 2 {
				write("ERR INVALID-ARGUMENT")
				continue
			}
			if user == "" {
				write("ERR USERNAME-REQUIRED")
				continue
			}
			// upsd answers OK to any password and checks it only at an action (SET, LOGIN,
			// FSD); a wrong password on a read-only connection is never noticed. The fake does
			// the same, and RequireLogin stands for a server that refuses reads without a
			// known login.
			f.mu.Lock()
			pw, known := f.users[user]
			f.mu.Unlock()
			loggedIn = known && pw == fields[1]
			write("OK")
		case "VER":
			write(f.Version)
		case "NETVER":
			write("1.3")
		case "LOGOUT":
			write("OK Goodbye")
			return
		case "LIST":
			if f.RequireLogin && !loggedIn {
				write("ERR ACCESS-DENIED")
				continue
			}
			if len(fields) < 2 {
				write("ERR INVALID-ARGUMENT")
				continue
			}
			switch strings.ToUpper(fields[1]) {
			case "UPS":
				f.mu.Lock()
				names := make([]string, 0, len(f.ups))
				for n := range f.ups {
					names = append(names, n)
				}
				sort.Strings(names)
				lines := []string{"BEGIN LIST UPS"}
				for _, n := range names {
					lines = append(lines, fmt.Sprintf("UPS %s %s", n, Quote(f.ups[n].desc)))
				}
				f.mu.Unlock()
				lines = append(lines, "END LIST UPS")
				write(strings.Join(lines, "\n"))
			case "VAR":
				if len(fields) < 3 {
					write("ERR INVALID-ARGUMENT")
					continue
				}
				f.mu.Lock()
				u := f.ups[fields[2]]
				if u == nil {
					f.mu.Unlock()
					write("ERR UNKNOWN-UPS")
					continue
				}
				if u.stale {
					f.mu.Unlock()
					write("ERR DATA-STALE")
					continue
				}
				names := make([]string, 0, len(u.vars))
				for n := range u.vars {
					names = append(names, n)
				}
				sort.Strings(names)
				lines := []string{"BEGIN LIST VAR " + fields[2]}
				for _, n := range names {
					lines = append(lines, fmt.Sprintf("VAR %s %s %s", fields[2], n, Quote(u.vars[n])))
				}
				f.mu.Unlock()
				lines = append(lines, "END LIST VAR "+fields[2])
				write(strings.Join(lines, "\n"))
			default:
				write("ERR INVALID-ARGUMENT")
			}
		case "GET":
			if len(fields) < 4 || strings.ToUpper(fields[1]) != "VAR" {
				write("ERR INVALID-ARGUMENT")
				continue
			}
			f.mu.Lock()
			u := f.ups[fields[2]]
			if u == nil {
				f.mu.Unlock()
				write("ERR UNKNOWN-UPS")
				continue
			}
			if u.stale {
				f.mu.Unlock()
				write("ERR DATA-STALE")
				continue
			}
			v, ok := u.vars[fields[3]]
			f.mu.Unlock()
			if !ok {
				write("ERR VAR-NOT-SUPPORTED")
				continue
			}
			write(fmt.Sprintf("VAR %s %s %s", fields[2], fields[3], Quote(v)))
		default:
			write("ERR UNKNOWN-COMMAND")
		}
	}
}

// DemoVars are the variables of a typical USB UPS, for the fake.
func DemoVars(status string, charge, runtime int) map[string]string {
	return map[string]string{
		"battery.charge":        fmt.Sprint(charge),
		"battery.charge.low":    "20",
		"battery.runtime":       fmt.Sprint(runtime),
		"battery.runtime.low":   "120",
		"battery.voltage":       "27.1",
		"battery.type":          "PbAc",
		"device.mfr":            "EATON",
		"device.model":          "Ellipse ECO 650",
		"device.type":           "ups",
		"driver.name":           "usbhid-ups",
		"driver.version":        "2.8.2",
		"input.voltage":         "231.0",
		"input.transfer.high":   "264",
		"input.transfer.low":    "184",
		"output.voltage":        "230.0",
		"ups.beeper.status":     "enabled",
		"ups.load":              "23",
		"ups.mfr":               "EATON",
		"ups.model":             "Ellipse ECO 650",
		"ups.serial":            "000000000",
		"ups.status":            status,
		"ups.test.result":       "Done and passed",
		"ups.timer.shutdown":    "-1",
		"ups.productid":         "ffff",
		"ups.vendorid":          "0463",
		"ups.firmware":          "02",
		"ups.power.nominal":     "650",
		"ups.realpower.nominal": "400",
	}
}

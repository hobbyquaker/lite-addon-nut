// Package nut speaks the Network UPS Tools protocol to an upsd server (TCP 3493): the connection
// with optional STARTTLS and login, LIST UPS, LIST VAR and GET VAR, and the parsing of ups.status
// into its flags. It is a client only; nothing here listens.
//
// The protocol is a line protocol: one command per line, answers as single lines ("OK", "ERR
// <code> [extra]", "VAR <ups> <name> "<value>"") or as lists between "BEGIN LIST …" and "END LIST
// …". Values are double-quoted with \" and \\ as the only escapes.
package nut

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// DefaultPort is upsd's port.
const DefaultPort = 3493

// Options describe one upsd server.
type Options struct {
	Host string
	// Port is 3493 when 0.
	Port int
	// Username and Password log in after the connection (upsd.users); both empty: no login.
	Username, Password string
	// TLS asks for STARTTLS after the connection: "off" (plain), "starttls" (required: the
	// connection fails when the server does not offer it).
	TLS string
	// InsecureSkipVerify accepts any server certificate (a self-signed upsd without a CA).
	InsecureSkipVerify bool
	// CACert is a PEM bundle the server's certificate must chain to; empty: the system roots.
	CACert string
	// ServerName overrides the name verified in the certificate (the host by default).
	ServerName string
	// Timeout bounds the connection and every command; 10 s when 0.
	Timeout time.Duration
}

// TLS modes.
const (
	TLSOff      = "off"
	TLSStartTLS = "starttls"
)

func (o Options) port() int {
	if o.Port <= 0 {
		return DefaultPort
	}
	return o.Port
}

func (o Options) timeout() time.Duration {
	if o.Timeout <= 0 {
		return 10 * time.Second
	}
	return o.Timeout
}

// Addr is host:port.
func (o Options) Addr() string { return net.JoinHostPort(o.Host, strconv.Itoa(o.port())) }

// Error is an ERR answer of the server: the code as upsd spells it (ACCESS-DENIED, UNKNOWN-UPS,
// DATA-STALE, DRIVER-NOT-CONNECTED, FEATURE-NOT-CONFIGURED, …) and what followed it.
type Error struct {
	Code  string
	Extra string
}

func (e *Error) Error() string {
	if e.Extra != "" {
		return "upsd: " + e.Code + " " + e.Extra
	}
	return "upsd: " + e.Code
}

// IsCode reports whether err is a server error with that code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// Conn is one connection to upsd.
type Conn struct {
	opts  Options
	conn  net.Conn
	r     *bufio.Reader
	tls   bool
	Debug func(dir, line string)
}

// Dial connects, negotiates STARTTLS when asked and logs in when a username is set.
func Dial(ctx context.Context, o Options) (*Conn, error) {
	if o.Host == "" {
		return nil, errors.New("no host")
	}
	d := net.Dialer{Timeout: o.timeout()}
	raw, err := d.DialContext(ctx, "tcp", o.Addr())
	if err != nil {
		return nil, err
	}
	c := &Conn{opts: o, conn: raw, r: bufio.NewReaderSize(raw, 64*1024)}
	if err := c.setup(ctx); err != nil {
		c.conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *Conn) setup(ctx context.Context) error {
	if c.opts.TLS == TLSStartTLS {
		if err := c.startTLS(ctx); err != nil {
			return err
		}
	}
	if c.opts.Username != "" {
		if err := c.ok(ctx, "USERNAME "+c.opts.Username); err != nil {
			return fmt.Errorf("login: %w", err)
		}
		if err := c.ok(ctx, "PASSWORD "+c.opts.Password); err != nil {
			return fmt.Errorf("login: %w", err)
		}
	}
	return nil
}

func (c *Conn) startTLS(ctx context.Context) error {
	if err := c.ok(ctx, "STARTTLS"); err != nil {
		return fmt.Errorf("STARTTLS: %w", err)
	}
	cfg := &tls.Config{ServerName: c.opts.ServerName, InsecureSkipVerify: c.opts.InsecureSkipVerify, MinVersion: tls.VersionTLS12}
	if cfg.ServerName == "" {
		cfg.ServerName = c.opts.Host
	}
	if c.opts.CACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(c.opts.CACert)) {
			return errors.New("the CA certificate is not PEM")
		}
		cfg.RootCAs = pool
	}
	tc := tls.Client(c.conn, cfg)
	hctx, cancel := context.WithTimeout(ctx, c.opts.timeout())
	defer cancel()
	if err := tc.HandshakeContext(hctx); err != nil {
		return fmt.Errorf("TLS handshake: %w", err)
	}
	c.conn = tc
	c.r = bufio.NewReaderSize(tc, 64*1024)
	c.tls = true
	return nil
}

// TLS reports whether the connection is encrypted.
func (c *Conn) TLS() bool { return c.tls }

// Close says LOGOUT (best effort) and closes the connection.
func (c *Conn) Close() error {
	_ = c.conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = c.conn.Write([]byte("LOGOUT\n"))
	return c.conn.Close()
}

func (c *Conn) send(ctx context.Context, line string) error {
	if c.Debug != nil {
		shown := line
		if strings.HasPrefix(line, "PASSWORD ") {
			shown = "PASSWORD ••••"
		}
		c.Debug(">", shown)
	}
	dl := time.Now().Add(c.opts.timeout())
	if d, ok := ctx.Deadline(); ok && d.Before(dl) {
		dl = d
	}
	if err := c.conn.SetDeadline(dl); err != nil {
		return err
	}
	_, err := c.conn.Write([]byte(line + "\n"))
	return err
}

func (c *Conn) readLine() (string, error) {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if c.Debug != nil {
		c.Debug("<", line)
	}
	return line, nil
}

// errOf turns an ERR line into an Error, nil for any other line.
func errOf(line string) error {
	if !strings.HasPrefix(line, "ERR") {
		return nil
	}
	f := strings.Fields(line)
	e := &Error{Code: "UNKNOWN"}
	if len(f) > 1 {
		e.Code = f[1]
	}
	if len(f) > 2 {
		e.Extra = strings.Join(f[2:], " ")
	}
	return e
}

// ok sends a command that answers OK.
func (c *Conn) ok(ctx context.Context, cmd string) error {
	if err := c.send(ctx, cmd); err != nil {
		return err
	}
	line, err := c.readLine()
	if err != nil {
		return err
	}
	if err := errOf(line); err != nil {
		return err
	}
	if !strings.HasPrefix(line, "OK") {
		return fmt.Errorf("unexpected answer %q", line)
	}
	return nil
}

// list sends LIST <what> and returns the lines between BEGIN and END, with the "LIST <what>"
// prefix and the item keyword still in each line.
func (c *Conn) list(ctx context.Context, what string) ([]string, error) {
	if err := c.send(ctx, "LIST "+what); err != nil {
		return nil, err
	}
	first, err := c.readLine()
	if err != nil {
		return nil, err
	}
	if err := errOf(first); err != nil {
		return nil, err
	}
	if first != "BEGIN LIST "+what {
		return nil, fmt.Errorf("unexpected answer %q", first)
	}
	var out []string
	for {
		line, err := c.readLine()
		if err != nil {
			return nil, err
		}
		if line == "END LIST "+what {
			return out, nil
		}
		if err := errOf(line); err != nil {
			return nil, err
		}
		out = append(out, line)
		if len(out) > 10000 {
			return nil, errors.New("list too long")
		}
	}
}

// UPS is one entry of LIST UPS.
type UPS struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ListUPS asks for the UPS the server knows.
func (c *Conn) ListUPS(ctx context.Context) ([]UPS, error) {
	lines, err := c.list(ctx, "UPS")
	if err != nil {
		return nil, err
	}
	var out []UPS
	for _, l := range lines {
		f, err := Fields(l)
		if err != nil || len(f) < 2 || f[0] != "UPS" {
			return nil, fmt.Errorf("bad line %q", l)
		}
		u := UPS{Name: f[1]}
		if len(f) > 2 {
			u.Description = f[2]
		}
		out = append(out, u)
	}
	return out, nil
}

// ListVar asks for every variable of one UPS.
func (c *Conn) ListVar(ctx context.Context, ups string) (map[string]string, error) {
	lines, err := c.list(ctx, "VAR "+ups)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(lines))
	for _, l := range lines {
		f, err := Fields(l)
		if err != nil || len(f) < 4 || f[0] != "VAR" || f[1] != ups {
			return nil, fmt.Errorf("bad line %q", l)
		}
		out[f[2]] = f[3]
	}
	return out, nil
}

// GetVar asks for one variable.
func (c *Conn) GetVar(ctx context.Context, ups, name string) (string, error) {
	if err := c.send(ctx, "GET VAR "+ups+" "+name); err != nil {
		return "", err
	}
	line, err := c.readLine()
	if err != nil {
		return "", err
	}
	if err := errOf(line); err != nil {
		return "", err
	}
	f, err := Fields(line)
	if err != nil || len(f) < 4 || f[0] != "VAR" {
		return "", fmt.Errorf("unexpected answer %q", line)
	}
	return f[3], nil
}

// Version asks for the server's version line (VER).
func (c *Conn) Version(ctx context.Context) (string, error) {
	if err := c.send(ctx, "VER"); err != nil {
		return "", err
	}
	line, err := c.readLine()
	if err != nil {
		return "", err
	}
	if err := errOf(line); err != nil {
		return "", err
	}
	return line, nil
}

// Fields splits a protocol line into its words, unquoting "…" values (\" and \\ escapes).
func Fields(line string) ([]string, error) {
	var out []string
	i := 0
	for i < len(line) {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= len(line) {
			break
		}
		if line[i] == '"' {
			i++
			var b strings.Builder
			closed := false
			for i < len(line) {
				ch := line[i]
				if ch == '\\' && i+1 < len(line) {
					b.WriteByte(line[i+1])
					i += 2
					continue
				}
				if ch == '"' {
					i++
					closed = true
					break
				}
				b.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quote in %q", line)
			}
			out = append(out, b.String())
			continue
		}
		j := strings.IndexByte(line[i:], ' ')
		if j < 0 {
			out = append(out, line[i:])
			break
		}
		out = append(out, line[i:i+j])
		i += j
	}
	return out, nil
}

// Quote writes a value as upsd does.
func Quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// Probe connects, logs in, lists the UPS and the version, and closes again: the Test button.
type ProbeResult struct {
	Version string `json:"version"`
	TLS     bool   `json:"tls"`
	UPS     []UPS  `json:"ups"`
}

// Probe is one round trip for the Test connection button.
func Probe(ctx context.Context, o Options) (*ProbeResult, error) {
	c, err := Dial(ctx, o)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	res := &ProbeResult{TLS: c.TLS()}
	res.Version, _ = c.Version(ctx)
	res.UPS, err = c.ListUPS(ctx)
	if err != nil {
		return nil, err
	}
	if res.UPS == nil {
		res.UPS = []UPS{}
	}
	return res, nil
}

package nut

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestFields(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
		err  bool
	}{
		{`VAR ups ups.status "OL CHRG"`, []string{"VAR", "ups", "ups.status", "OL CHRG"}, false},
		{`UPS ups "Eaton \"Ellipse\" \\ back"`, []string{"UPS", "ups", `Eaton "Ellipse" \ back`}, false},
		{`OK`, []string{"OK"}, false},
		{`  a   b  `, []string{"a", "b"}, false},
		{`VAR ups x ""`, []string{"VAR", "ups", "x", ""}, false},
		{`VAR ups x "open`, nil, true},
	} {
		got, err := Fields(tc.in)
		if (err != nil) != tc.err {
			t.Errorf("%q: err %v", tc.in, err)
			continue
		}
		if !tc.err && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: %q, want %q", tc.in, got, tc.want)
		}
	}
	if q := Quote(`a "b" \c`); q != `"a \"b\" \\c"` {
		t.Errorf("Quote: %s", q)
	}
}

func TestParseStatus(t *testing.T) {
	s := ParseStatus("ob lb DISCHRG OB")
	if s.String() != "OB LB DISCHRG" || !s.OnBattery() || s.Online() || !s.Has(FlagLowBattery) {
		t.Errorf("%+v", s)
	}
	if !ParseStatus("OL CHRG").Online() {
		t.Error("OL CHRG is online")
	}
	if ParseStatus("OL OB").Online() {
		t.Error("OL OB is not online")
	}
	if v, ok := Float(map[string]string{"a": " 12.5"}, "a"); !ok || v != 12.5 {
		t.Error("Float")
	}
	if _, ok := Float(map[string]string{"a": "x"}, "a"); ok {
		t.Error("Float of x")
	}
}

func selfSigned(t *testing.T) (tls.Certificate, string) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "upsd.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"upsd.test"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return c, string(certPEM)
}

func TestClientAgainstFake(t *testing.T) {
	f, err := NewFakeUPSD("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.AddUPS("eaton", `Eaton "Ellipse"`, DemoVars("OL CHRG", 100, 1800))
	f.AddUPS("apc", "APC Back-UPS", DemoVars("OB DISCHRG", 60, 600))
	f.AddUser("monuser", "secret")
	ctx := context.Background()
	o := Options{Host: "127.0.0.1", Port: f.Port(), Timeout: 2 * time.Second}

	t.Run("plain", func(t *testing.T) {
		res, err := Probe(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		if res.TLS || len(res.UPS) != 2 || res.UPS[0].Name != "apc" || res.UPS[1].Description != `Eaton "Ellipse"` || res.Version == "" {
			t.Errorf("%+v", res)
		}
		c, err := Dial(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		vars, err := c.ListVar(ctx, "apc")
		if err != nil {
			t.Fatal(err)
		}
		if vars["ups.status"] != "OB DISCHRG" || len(vars) != len(DemoVars("", 0, 0)) {
			t.Errorf("vars %v", vars)
		}
		v, err := c.GetVar(ctx, "eaton", "battery.charge")
		if err != nil || v != "100" {
			t.Errorf("GetVar %q %v", v, err)
		}
		if _, err := c.ListVar(ctx, "nope"); !IsCode(err, "UNKNOWN-UPS") {
			t.Errorf("unknown ups: %v", err)
		}
		f.SetStale("apc", true)
		if _, err := c.ListVar(ctx, "apc"); !IsCode(err, "DATA-STALE") {
			t.Errorf("stale: %v", err)
		}
		f.SetStale("apc", false)
	})

	t.Run("login", func(t *testing.T) {
		f.RequireLogin = true
		defer func() { f.RequireLogin = false }()
		if _, err := Probe(ctx, o); !IsCode(err, "ACCESS-DENIED") {
			t.Errorf("without login: %v", err)
		}
		lo := o
		lo.Username, lo.Password = "monuser", "wrong"
		if _, err := Probe(ctx, lo); !IsCode(err, "ACCESS-DENIED") {
			t.Errorf("wrong password on a server that requires a login: %v", err)
		}
		lo.Password = "secret"
		if res, err := Probe(ctx, lo); err != nil || len(res.UPS) != 2 {
			t.Errorf("login: %v %+v", err, res)
		}
	})

	t.Run("starttls", func(t *testing.T) {
		to := o
		to.TLS = TLSStartTLS
		to.InsecureSkipVerify = true
		if _, err := Probe(ctx, to); !IsCode(err, "FEATURE-NOT-CONFIGURED") {
			t.Errorf("server without TLS: %v", err)
		}
		cert, pemCert := selfSigned(t)
		f.SetCert(cert)
		res, err := Probe(ctx, to)
		if err != nil || !res.TLS || len(res.UPS) != 2 {
			t.Fatalf("insecure: %v %+v", err, res)
		}
		to.InsecureSkipVerify = false
		if _, err := Probe(ctx, to); err == nil {
			t.Error("an unknown self-signed certificate must be refused")
		}
		to.CACert = pemCert
		if res, err := Probe(ctx, to); err != nil || !res.TLS {
			t.Errorf("pinned CA: %v", err)
		}
		to.ServerName = "other.test"
		if _, err := Probe(ctx, to); err == nil {
			t.Error("a wrong server name must be refused")
		}
	})

	t.Run("timeout", func(t *testing.T) {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		defer ln.Close()
		go func() {
			c, err := ln.Accept()
			if err == nil {
				time.Sleep(2 * time.Second)
				c.Close()
			}
		}()
		mute := Options{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Timeout: 300 * time.Millisecond}
		start := time.Now()
		if _, err := Probe(ctx, mute); err == nil {
			t.Error("a mute server must time out")
		}
		if time.Since(start) > 1500*time.Millisecond {
			t.Error("the timeout took too long")
		}
	})
}

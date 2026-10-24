package email

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal SMTP server on 127.0.0.1. It answers until it
// reaches stallAt and from then on holds the connection open without a
// word, the way a hung server does.
type fakeSMTP struct {
	t       *testing.T
	ln      net.Listener
	tls     *tls.Config // set: offers STARTTLS (or speaks TLS at once, see implicitTLS)
	implTLS bool
	// stallAt is where the server stops answering: "greeting", "handshake"
	// (the TLS handshake), a command such as "EHLO" or "MAIL", or "." (the
	// end of DATA). Empty: it never stalls.
	stallAt string

	mu sync.Mutex
	// log is the commands received, AUTH decoded; AUTH and MAIL also
	// note whether TLS was on
	log  []string
	data string
}

func startFakeSMTP(t *testing.T, s *fakeSMTP) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.t, s.ln = t, ln
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

// transport returns a transport aimed at the server, trusting its
// certificate.
func (s *fakeSMTP) transport(timeout time.Duration, clientTLS *tls.Config) transport {
	addr := s.ln.Addr().(*net.TCPAddr)
	return transport{
		host:        "127.0.0.1",
		port:        addr.Port,
		username:    "smtp-user",
		password:    testPassword,
		implicitTLS: s.implTLS,
		timeout:     timeout,
		tlsConfig:   clientTLS,
	}
}

func (s *fakeSMTP) record(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, line)
}

func (s *fakeSMTP) received() ([]string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.log...), s.data
}

// stall keeps conn open until the client gives up and closes it.
func stall(conn net.Conn) {
	_, _ = io.Copy(io.Discard, conn)
}

func (s *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	if s.implTLS {
		if s.stallAt == "handshake" {
			stall(conn)
			return
		}
		conn = tls.Server(conn, s.tls)
	}
	if s.stallAt == "greeting" {
		stall(conn)
		return
	}

	r := bufio.NewReader(conn)
	reply := func(lines ...string) {
		for _, l := range lines {
			_, _ = io.WriteString(conn, l+"\r\n")
		}
	}
	reply("220 fake ESMTP")

	secure := s.implTLS
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		if verb == s.stallAt {
			stall(conn)
			return
		}

		switch verb {
		case "EHLO":
			s.record(line)
			if s.tls != nil && !secure {
				reply("250-fake", "250-STARTTLS", "250 AUTH PLAIN")
			} else {
				reply("250-fake", "250 AUTH PLAIN")
			}
		case "STARTTLS":
			s.record(line)
			reply("220 go ahead")
			if s.stallAt == "handshake" {
				stall(conn)
				return
			}
			conn = tls.Server(conn, s.tls)
			r = bufio.NewReader(conn)
			secure = true
		case "AUTH":
			// AUTH PLAIN <base64("\x00user\x00password")>
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
			s.record("AUTH " + strings.ReplaceAll(string(raw), "\x00", "|") + " secure=" + strconv.FormatBool(secure))
			reply("235 ok")
		case "MAIL":
			s.record(line + " secure=" + strconv.FormatBool(secure))
			reply("250 ok")
		case "RCPT":
			s.record(line)
			reply("250 ok")
		case "DATA":
			s.record(line)
			reply("354 go on")
			var data strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(l)
			}
			s.mu.Lock()
			s.data = data.String()
			s.mu.Unlock()
			if s.stallAt == "." {
				stall(conn)
				return
			}
			reply("250 queued")
		case "QUIT":
			s.record(line)
			reply("221 bye")
			return
		default:
			reply("502 unknown command")
		}
	}
}

// testTLS returns a server config with a self-signed certificate for
// 127.0.0.1 and a client config that trusts it.
func testTLS(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)

	server = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	client = &tls.Config{RootCAs: roots, ServerName: "127.0.0.1"}
	return server, client
}

type stringMessage string

func (m stringMessage) WriteTo(w io.Writer) (int64, error) {
	n, err := io.WriteString(w, string(m))
	return int64(n), err
}

const testMessage = "Subject: hello\r\n\r\nbody\r\n"

func TestTransport_Delivers(t *testing.T) {
	serverTLS, clientTLS := testTLS(t)

	tests := []struct {
		name   string
		server *fakeSMTP
		noAuth bool // no credentials: the client must not AUTH, though offered
		// wantSecure: the message went over TLS
		wantSecure bool
	}{
		{name: "STARTTLS", server: &fakeSMTP{tls: serverTLS}, wantSecure: true},
		{name: "implicit TLS", server: &fakeSMTP{tls: serverTLS, implTLS: true}, wantSecure: true},
		{name: "STARTTLS without credentials", server: &fakeSMTP{tls: serverTLS}, noAuth: true, wantSecure: true},
		// Plain text is allowed to this machine without credentials only:
		// a local Mailpit offers no STARTTLS
		{name: "plain text to loopback without credentials", server: &fakeSMTP{}, noAuth: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := startFakeSMTP(t, tt.server)
			tr := srv.transport(5*time.Second, clientTLS)
			if tt.noAuth {
				tr.username, tr.password = "", ""
			}

			if err := tr.send("noreply@example.com", "ada@example.com", stringMessage(testMessage)); err != nil {
				t.Fatalf("send() error = %v", err)
			}

			log, data := srv.received()
			want := []string{
				"MAIL FROM:<noreply@example.com> secure=" + strconv.FormatBool(tt.wantSecure),
				"RCPT TO:<ada@example.com>",
				"DATA",
				"QUIT",
			}
			if !tt.noAuth {
				want = append(want, "AUTH |smtp-user|"+testPassword+" secure=true")
			}
			got := strings.Join(log, "\n")
			for _, w := range want {
				if !strings.Contains(got, w) {
					t.Errorf("server log does not contain %q:\n%s", w, got)
				}
			}
			if tt.noAuth && strings.Contains(got, "AUTH") {
				t.Errorf("client authenticated without credentials:\n%s", got)
			}
			if data != testMessage {
				t.Errorf("DATA = %q, want %q", data, testMessage)
			}
		})
	}
}

// A server without STARTTLS gets neither the credentials nor the message,
// unless the session may be plain text (no credentials, this machine).
func TestTransport_RefusesPlaintext(t *testing.T) {
	// It offers AUTH PLAIN, but no STARTTLS
	srv := startFakeSMTP(t, &fakeSMTP{})

	err := srv.transport(5*time.Second, nil).send("noreply@example.com", "ada@example.com", stringMessage(testMessage))
	if !errors.Is(err, errNoTLS) {
		t.Fatalf("send() error = %v, want %v", err, errNoTLS)
	}

	log, data := srv.received()
	for _, l := range log {
		if strings.HasPrefix(l, "AUTH") || strings.HasPrefix(l, "MAIL") {
			t.Errorf("client sent %q over plain text", l)
		}
	}
	if data != "" {
		t.Errorf("DATA = %q, want nothing", data)
	}
}

func TestTransport_PlaintextAllowed(t *testing.T) {
	tests := []struct {
		host, username string
		want           bool
	}{
		{"localhost", "", true},
		{"LocalHost", "", true},
		{"127.0.0.1", "", true},
		{"127.10.20.30", "", true},
		{"::1", "", true},
		// Credentials never go in plain text, not even to this machine
		{"localhost", "smtp-user", false},
		{"127.0.0.1", "smtp-user", false},
		{"::1", "smtp-user", false},
		// Any name or address that may be another machine needs TLS
		{"smtp.example.com", "", false},
		{"mailpit", "", false},
		{"localhost.example.com", "", false},
		{"10.0.0.5", "", false},
		{"0.0.0.0", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		tr := transport{host: tt.host, username: tt.username}
		if got := tr.plaintextAllowed(); got != tt.want {
			t.Errorf("plaintextAllowed() for host %q, username %q = %v, want %v", tt.host, tt.username, got, tt.want)
		}
	}
}

// A server that accepts the connection and then stalls — at any step —
// must not hold the sender past its deadline.
func TestTransport_StalledServerTimesOut(t *testing.T) {
	serverTLS, clientTLS := testTLS(t)
	const timeout = 300 * time.Millisecond

	tests := []struct {
		name   string
		server *fakeSMTP
	}{
		{name: "no greeting", server: &fakeSMTP{stallAt: "greeting"}},
		{name: "EHLO unanswered", server: &fakeSMTP{stallAt: "EHLO"}},
		{name: "STARTTLS handshake", server: &fakeSMTP{tls: serverTLS, stallAt: "handshake"}},
		{name: "implicit TLS handshake", server: &fakeSMTP{tls: serverTLS, implTLS: true, stallAt: "handshake"}},
		{name: "AUTH unanswered", server: &fakeSMTP{tls: serverTLS, stallAt: "AUTH"}},
		{name: "end of DATA unanswered", server: &fakeSMTP{tls: serverTLS, stallAt: "."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := startFakeSMTP(t, tt.server)

			start := time.Now()
			err := srv.transport(timeout, clientTLS).send("noreply@example.com", "ada@example.com", stringMessage(testMessage))
			elapsed := time.Since(start)

			if !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Errorf("send() error = %v, want a deadline error", err)
			}
			if elapsed > timeout+time.Second {
				t.Errorf("send() returned after %s, want about %s", elapsed, timeout)
			}
		})
	}
}

func TestLoginAuth(t *testing.T) {
	a := &loginAuth{username: "smtp-user", password: testPassword}

	for _, host := range []string{"smtp.example.com", "localhost", "127.0.0.1"} {
		if _, _, err := a.Start(&smtp.ServerInfo{Name: host, TLS: false, Auth: []string{"LOGIN"}}); err == nil {
			t.Errorf("Start() over plain text to %s: error = nil, want a refusal", host)
		}
	}
	if mech, _, err := a.Start(&smtp.ServerInfo{Name: "smtp.example.com", TLS: true}); err != nil || mech != "LOGIN" {
		t.Errorf("Start() over TLS = %q, %v; want LOGIN", mech, err)
	}

	for challenge, want := range map[string]string{"Username:": "smtp-user", "Password:": testPassword} {
		got, err := a.Next([]byte(challenge), true)
		if err != nil || string(got) != want {
			t.Errorf("Next(%q) = %q, %v; want %q", challenge, got, err, want)
		}
	}
	if _, err := a.Next([]byte("Something:"), true); err == nil {
		t.Error("Next(unknown challenge) error = nil")
	}
}

func TestTransport_AuthMechanism(t *testing.T) {
	tr := transport{host: "smtp.example.com", username: "u", password: "p"}

	tests := []struct {
		advertised string
		want       string
	}{
		{"PLAIN LOGIN", "PLAIN"},
		{"LOGIN XOAUTH2", "LOGIN"},
		{"CRAM-MD5 PLAIN", "CRAM-MD5"},
	}
	for _, tt := range tests {
		mech, _, err := tr.auth(tt.advertised).Start(&smtp.ServerInfo{Name: "smtp.example.com", TLS: true})
		if err != nil || mech != tt.want {
			t.Errorf("auth(%q) starts %q, %v; want %q", tt.advertised, mech, err, tt.want)
		}
	}
}

// NewSMTPEmailService delivers through the transport, with the envelope
// taken from the email's sender (a notice: NotificationsFromEmail, which
// follows FromEmail when unset) and the parsed recipient. Without credentials it
// works with a local server that offers no TLS, as Mailpit does.
func TestSMTPEmailService_DeliversOverSMTP(t *testing.T) {
	srv := startFakeSMTP(t, &fakeSMTP{})
	cfg := validConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = srv.ln.Addr().(*net.TCPAddr).Port
	cfg.Username, cfg.Password = "", ""

	service, err := NewSMTPEmailService(cfg)
	if err != nil {
		t.Fatalf("NewSMTPEmailService() error = %v", err)
	}
	if err := service.SendWelcomeEmail("Ada <ada@example.com>", "Ada"); err != nil {
		t.Fatalf("SendWelcomeEmail() error = %v", err)
	}

	log, data := srv.received()
	got := strings.Join(log, "\n")
	for _, w := range []string{"MAIL FROM:<noreply@example.com>", "RCPT TO:<ada@example.com>"} {
		if !strings.Contains(got, w) {
			t.Errorf("server log does not contain %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, "AUTH") {
		t.Errorf("client authenticated without credentials:\n%s", got)
	}
	if !strings.Contains(data, "Subject: Welcome to Timebox!") {
		t.Errorf("DATA has no welcome subject:\n%s", data)
	}
}

// With credentials, the same server is refused: the password would cross
// it in plain text.
func TestSMTPEmailService_CredentialsNeedTLS(t *testing.T) {
	srv := startFakeSMTP(t, &fakeSMTP{})
	cfg := validConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = srv.ln.Addr().(*net.TCPAddr).Port

	service, err := NewSMTPEmailService(cfg)
	if err != nil {
		t.Fatalf("NewSMTPEmailService() error = %v", err)
	}
	if err := service.SendWelcomeEmail("ada@example.com", "Ada"); !errors.Is(err, errNoTLS) {
		t.Errorf("SendWelcomeEmail() error = %v, want %v", err, errNoTLS)
	}
	if log, data := srv.received(); strings.Contains(strings.Join(log, "\n"), "AUTH") || data != "" {
		t.Errorf("server received AUTH or a message over plain text:\n%s", strings.Join(log, "\n"))
	}
}

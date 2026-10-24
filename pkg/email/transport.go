package email

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// sendTimeout bounds one whole delivery: DNS, TCP, TLS or STARTTLS, AUTH,
// DATA and QUIT together. gomail only bounded the dial, so a server that
// accepted the connection and then stalled held the sender forever.
const sendTimeout = 30 * time.Second

// implicitTLSPort is the SMTPS port, where TLS starts with the first byte.
// Any other port upgrades with STARTTLS, which the server must offer
// unless the session may stay in plain text (see transport.plaintextAllowed).
const implicitTLSPort = 465

// errNoTLS is returned when the server offers no STARTTLS and the session
// may not continue in plain text (see transport.plaintextAllowed).
var errNoTLS = errors.New("email: SMTP server does not offer STARTTLS; refusing to send unencrypted")

// transport delivers one message per connection. It behaves like gomail's
// dialer — implicit TLS on 465, STARTTLS when offered, the AUTH mechanism
// picked from what the server advertises — but under one deadline, and it
// does not fall back to plain text except to a local server without
// credentials.
type transport struct {
	host     string
	port     int
	username string // empty: no AUTH
	password string

	implicitTLS bool
	timeout     time.Duration
	// tlsConfig is nil outside tests: the certificate is then verified
	// against host.
	tlsConfig *tls.Config
}

func newTransport(cfg Config) transport {
	return transport{
		host:        cfg.Host,
		port:        cfg.Port,
		username:    cfg.Username,
		password:    cfg.Password,
		implicitTLS: cfg.Port == implicitTLSPort,
		timeout:     sendTimeout,
	}
}

// send delivers msg from the envelope sender from to the recipient to.
func (t transport) send(from, to string, msg io.WriterTo) error {
	deadline := time.Now().Add(t.timeout)

	conn, err := (&net.Dialer{Deadline: deadline}).Dial("tcp", net.JoinHostPort(t.host, strconv.Itoa(t.port)))
	if err != nil {
		return err
	}
	defer conn.Close()
	// Every read and write past the deadline fails, whichever step the
	// server stalls in. TLS and STARTTLS wrap conn, so they inherit it.
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}

	if t.implicitTLS {
		conn = tls.Client(conn, t.clientTLS())
	}
	c, err := smtp.NewClient(conn, t.host)
	if err != nil {
		return err
	}
	defer c.Close()
	// EHLO now: Extension would hide its failure as a missing extension,
	// and a stalled server would then read as one without STARTTLS.
	if err := c.Hello("localhost"); err != nil {
		return err
	}

	if ok, _ := c.Extension("STARTTLS"); ok && !t.implicitTLS {
		if err := c.StartTLS(t.clientTLS()); err != nil {
			return err
		}
	}
	// Checked before AUTH and MAIL, so neither the credentials nor the
	// message leave over a connection that should have been encrypted.
	if _, encrypted := c.TLSConnectionState(); !encrypted && !t.plaintextAllowed() {
		return errNoTLS
	}
	if t.username != "" {
		if ok, mechanisms := c.Extension("AUTH"); ok {
			if err := c.Auth(t.auth(mechanisms)); err != nil {
				return err
			}
		}
	}

	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := msg.WriteTo(w); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	// The server accepted the message; a failed QUIT does not undo that.
	_ = c.Quit()
	return nil
}

// plaintextAllowed reports whether the session may go on without TLS: only
// without credentials, and only to a server on this machine, such as a
// local Mailpit, so nothing crosses the network in the clear. Everything
// else needs implicit TLS or STARTTLS.
func (t transport) plaintextAllowed() bool {
	return t.username == "" && isLoopback(t.host)
}

func (t transport) clientTLS() *tls.Config {
	if t.tlsConfig != nil {
		return t.tlsConfig
	}
	return &tls.Config{ServerName: t.host}
}

// auth picks the mechanism the way gomail did: CRAM-MD5 when offered,
// LOGIN when offered without PLAIN (Office 365), PLAIN otherwise.
func (t transport) auth(mechanisms string) smtp.Auth {
	switch {
	case strings.Contains(mechanisms, "CRAM-MD5"):
		return smtp.CRAMMD5Auth(t.username, t.password)
	case strings.Contains(mechanisms, "LOGIN") && !strings.Contains(mechanisms, "PLAIN"):
		return &loginAuth{username: t.username, password: t.password}
	default:
		return smtp.PlainAuth("", t.username, t.password, t.host)
	}
}

// loginAuth is the LOGIN mechanism. It does not send the password over a
// connection without TLS, not even to localhost (net/smtp's PlainAuth
// would): send never authenticates in plain text, and this keeps it so.
type loginAuth struct{ username, password string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("email: LOGIN refused over an unencrypted connection")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(string(fromServer)) {
	case "username:":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("email: unexpected LOGIN challenge %q", fromServer)
}

// isLoopback reports whether host names this machine: "localhost" or a
// loopback address (127.0.0.0/8, ::1).
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

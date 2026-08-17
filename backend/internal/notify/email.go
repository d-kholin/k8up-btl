package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Email sends plain-text mail over SMTP.
type Email struct {
	Host string
	Port int
	// TLSMode: "starttls" (default, port 587), "tls" (implicit, port 465), "none".
	TLSMode  string
	Username string
	Password string
	From     string
	To       []string
	// SubjectPrefix is prepended to every subject, e.g. "[k8up btl]".
	SubjectPrefix string
}

func (m *Email) Name() string { return "email" }

func (m *Email) Send(ctx context.Context, e Event) error {
	addr := net.JoinHostPort(m.Host, fmt.Sprintf("%d", m.Port))

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if m.TLSMode == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: m.Host})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}

	// smtp.Client has no context support; close the socket on ctx cancel so a
	// hung server cannot pin the goroutine past the manager's send timeout.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	c, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer c.Close()

	if m.TLSMode != "tls" && m.TLSMode != "none" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("smtp: server does not support STARTTLS (set SMTP_TLS=none to allow plaintext)")
		}
		if err := c.StartTLS(&tls.Config{ServerName: m.Host}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}

	if m.Username != "" {
		auth := smtp.PlainAuth("", m.Username, m.Password, m.Host)
		if ok, _ := c.Extension("AUTH"); !ok {
			return fmt.Errorf("smtp: credentials set but server offers no AUTH")
		}
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := c.Mail(m.From); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, rcpt := range m.To {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(m.message(e)); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return c.Quit()
}

func (m *Email) message(e Event) []byte {
	subject := e.Title
	if m.SubjectPrefix != "" {
		subject = m.SubjectPrefix + " " + subject
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", m.From)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(m.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString(strings.ReplaceAll(e.Body, "\n", "\r\n"))
	b.WriteString("\r\n")
	return []byte(b.String())
}

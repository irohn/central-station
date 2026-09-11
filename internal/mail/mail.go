// Package mail sends plain-text email over SMTP (verification links).
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

var ErrDisabled = errors.New("email sending is not configured")

// Client sends mail through one SMTP server. Zero Host disables it.
type Client struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// StartTLS upgrades the connection when the server offers it (default on).
	StartTLS bool
	Timeout  time.Duration
}

// Enabled reports whether a server is configured.
func (c *Client) Enabled() bool { return c != nil && c.Host != "" && c.From != "" }

// Send delivers a text/plain message.
func (c *Client) Send(ctx context.Context, to, subject, body string) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	addr := net.JoinHostPort(c.Host, fmt.Sprint(c.Port))
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp connect: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	var cl *smtp.Client
	if c.Port == 465 { // implicit TLS
		tconn := tls.Client(conn, &tls.Config{ServerName: c.Host})
		cl, err = smtp.NewClient(tconn, c.Host)
	} else {
		cl, err = smtp.NewClient(conn, c.Host)
	}
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	defer cl.Close()
	if c.StartTLS && c.Port != 465 {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(&tls.Config{ServerName: c.Host}); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}
	if c.Username != "" {
		if err := cl.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := cl.Mail(c.From); err != nil {
		return err
	}
	if err := cl.Rcpt(to); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	msg := strings.Join([]string{
		"From: " + c.From,
		"To: " + to,
		"Subject: " + subject,
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

package channel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type smtpSender struct {
	config    Config
	tlsConfig *tls.Config
}

func (s *smtpSender) tlsSettings(host string) *tls.Config {
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	if s.tlsConfig != nil {
		config = s.tlsConfig.Clone()
		config.MinVersion, config.ServerName = tls.VersionTLS12, host
	}
	return config
}

func (s *smtpSender) Channel() string                { return "email" }
func (s *smtpSender) Kind() string                   { return "smtp" }
func (s *smtpSender) Test(ctx context.Context) error { return s.Send(ctx, testMessage(s.config)) }

func testMessage(c Config) Message {
	return Message{Recipient: c["test_recipient"], Subject: "Notification test", Body: "Notification channel test", Format: "text"}
}

func (s *smtpSender) Send(ctx context.Context, m Message) (result error) {
	if validateAddress(m.Recipient) != nil || strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("invalid notification message")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	host, _, err := net.SplitHostPort(s.config["smtp_addr"])
	if err != nil {
		return errors.New("invalid SMTP endpoint")
	}
	dialer := &net.Dialer{}
	var conn net.Conn
	if s.config["tls_mode"] == "tls" {
		tlsDialer := tls.Dialer{NetDialer: dialer, Config: s.tlsSettings(host)}
		conn, err = tlsDialer.DialContext(ctx, "tcp", s.config["smtp_addr"])
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", s.config["smtp_addr"])
	}
	if err != nil {
		return errors.New("notification SMTP connection failed")
	}
	stop := context.AfterFunc(ctx, func() {
		// Cancellation closes the socket to unblock the SMTP protocol reader.
		if err := conn.Close(); err != nil {
			return
		}
	})
	defer stop()
	defer func() {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			result = errors.Join(result, smtpError(err))
		}
	}()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return errors.New("SMTP deadline failed")
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return errors.New("notification SMTP handshake failed")
	}
	if s.config["tls_mode"] != "tls" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if client.StartTLS(s.tlsSettings(host)) != nil {
				return errors.New("SMTP TLS failed")
			}
		} else if s.config["tls_mode"] == "starttls" || !loopback(host) {
			return errors.New("SMTP TLS required")
		}
	}
	if s.config["username"] != "" {
		if client.Auth(smtp.PlainAuth("", s.config["username"], s.config["password"], host)) != nil {
			return errors.New("SMTP authentication failed")
		}
	}
	return smtpError(s.deliver(client, m))
}

func (s *smtpSender) deliver(client *smtp.Client, m Message) error {
	if err := client.Mail(s.config["from_address"]); err != nil {
		return err
	}
	if err := client.Rcpt(m.Recipient); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	contentType := "text/plain"
	if m.Format == "html" {
		contentType = "text/html"
	}
	_, writeErr := fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: %s; charset=UTF-8\r\n\r\n%s\r\n", s.config["from_address"], m.Recipient, mime.QEncoding.Encode("utf-8", m.Subject), contentType, m.Body)
	if err := errors.Join(writeErr, writer.Close()); err != nil {
		return err
	}
	return client.Quit()
}

func smtpError(err error) error {
	if err != nil {
		return errors.New("notification SMTP delivery failed")
	}
	return nil
}

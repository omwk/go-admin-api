package notifier

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// Email resolves its recipient from the authorized account at delivery time.
type Email struct {
	Settings  Settings
	Recipient string
}

// Send requires TLS for remote SMTP servers and emits no business values.
func (e Email) Send(ctx context.Context, m Message) (result error) {
	if err := m.Validate(); err != nil {
		return err
	}
	if err := e.Settings.Validate(); err != nil {
		return err
	}
	address, err := mail.ParseAddress(e.Recipient)
	if !e.Settings.EmailEnabled || err != nil || address.Address != e.Recipient || strings.ContainsAny(e.Recipient, "\r\n") {
		return errors.New("invalid notification recipient")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "tcp", e.Settings.SMTPAddr)
	if err != nil {
		return errors.New("notification SMTP connection failed")
	}
	deadline, _ := ctx.Deadline()
	if err := connection.SetDeadline(deadline); err != nil {
		closeErr := connection.Close()
		return errors.Join(errors.New("notification SMTP deadline failed"), safeSMTPError(closeErr))
	}
	host, _, err := net.SplitHostPort(e.Settings.SMTPAddr)
	if err != nil {
		closeErr := connection.Close()
		return errors.Join(errors.New("invalid notification SMTP endpoint"), safeSMTPError(closeErr))
	}
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		closeErr := connection.Close()
		return errors.Join(errors.New("notification SMTP connection failed"), safeSMTPError(closeErr))
	}
	defer func() { result = errors.Join(result, safeSMTPError(client.Close())) }()
	return safeSMTPError(e.deliver(client, host, m))
}

func (e Email) deliver(client *smtp.Client, host string, m Message) error {
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	} else if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("SMTP TLS required")
	}
	if e.Settings.SMTPUser != "" {
		if err := client.Auth(smtp.PlainAuth("", e.Settings.SMTPUser, e.Settings.SMTPPassword, host)); err != nil {
			return err
		}
	}
	if err := client.Mail(e.Settings.SMTPFrom); err != nil {
		return err
	}
	if err := client.Rcpt(e.Recipient); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nSubject: Credential maintenance %s\r\nMessage-ID: <notification-%d@commercehub>\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n%s\r\n", e.Settings.SMTPFrom, e.Recipient, m.Severity, m.EventID, m.Template, m.Path)
	return errors.Join(writeErr, writer.Close())
}

func safeSMTPError(err error) error {
	if err != nil {
		return errors.New("notification email delivery failed")
	}
	return nil
}

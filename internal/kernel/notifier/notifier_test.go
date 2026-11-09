package notifier

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

// REQ-SYS-007: a real SMTP session receives only the approved projection.
func TestEmailDeliversMetadata(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()
	result := make(chan error, 1)
	body := make(chan string, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			result <- err
			return
		}
		defer func() { result <- connection.Close() }()
		require.NoError(t, connection.SetDeadline(time.Now().Add(5*time.Second)))
		reader := textproto.NewReader(bufio.NewReader(connection))
		if _, err := fmt.Fprint(connection, "220 fixture ESMTP\r\n"); err != nil {
			result <- err
			return
		}
		for {
			line, err := reader.ReadLine()
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				_, err = fmt.Fprint(connection, "250 fixture\r\n")
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				_, err = fmt.Fprint(connection, "250 ok\r\n")
			case line == "DATA":
				if _, err = fmt.Fprint(connection, "354 data\r\n"); err != nil {
					return
				}
				raw, readErr := reader.ReadDotBytes()
				if readErr != nil {
					result <- readErr
					return
				}
				body <- string(raw)
				_, err = fmt.Fprint(connection, "250 accepted\r\n")
			default:
				return
			}
			if err != nil {
				result <- err
				return
			}
		}
	}()
	message := Message{EventID: 71, Template: "credential.needs_reauth", Platform: "mock", Severity: "critical", Path: "/admin/notifications"}
	sender := Email{Settings: Settings{EmailEnabled: true, SMTPAddr: listener.Addr().String(), SMTPFrom: "maintenance@example.invalid"}, Recipient: "operator@example.invalid"}
	require.NoError(t, sender.Send(context.Background(), message))
	select {
	case raw := <-body:
		require.Contains(t, raw, message.Template)
		require.Contains(t, raw, message.Path)
		require.NotContains(t, raw, "credential_id")
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP message missing")
	}
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP session did not close")
	}
}

func TestMessageAndSettingsRejectUnsafeInput(t *testing.T) {
	message := Message{EventID: 1, Template: "credential.unknown", Severity: "critical", Path: "https://invalid.example"}
	require.Error(t, message.Validate())
	for _, endpoint := range []string{"http://remote.example", "https://user:pass@remote.example", "file:///tmp/notify"} {
		require.Error(t, (Settings{WebhookEnabled: true, WebhookURL: endpoint}).Validate())
	}
	require.Error(t, (Settings{EmailEnabled: true, SMTPAddr: "127.0.0.1:1", SMTPFrom: "x@example.invalid\r\nBcc: y@example.invalid"}).Validate())
}

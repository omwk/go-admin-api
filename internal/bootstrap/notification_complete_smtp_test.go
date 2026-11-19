package bootstrap

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type completeMail struct{ Recipient, Body string }

func completeSMTP(t *testing.T) (string, <-chan completeMail) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	messages := make(chan completeMail, 20)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			completeSMTPConnection(conn, messages)
		}
	}()
	t.Cleanup(func() { require.NoError(t, listener.Close()); <-done })
	return listener.Addr().String(), messages
}

func completeSMTPConnection(conn net.Conn, messages chan<- completeMail) {
	defer func() {
		if err := conn.Close(); err != nil {
			return
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return
	}
	write := func(s string) bool { _, err := io.WriteString(conn, s); return err == nil }
	if !write("220 local\r\n") {
		return
	}
	r := bufio.NewReader(conn)
	msg := completeMail{}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "RCPT TO:"):
			msg.Recipient = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "RCPT TO:")), "<>")
			if !write("250 ok\r\n") {
				return
			}
		case strings.HasPrefix(line, "DATA"):
			if !write("354 data\r\n") {
				return
			}
			var b strings.Builder
			for {
				line, err = r.ReadString('\n')
				if err != nil {
					return
				}
				if line == ".\r\n" {
					break
				}
				b.WriteString(line)
			}
			msg.Body = b.String()
			messages <- msg
			if !write("250 received\r\n") {
				return
			}
		case strings.HasPrefix(line, "QUIT"):
			write("221 bye\r\n")
			return
		default:
			if !write("250 local\r\n") {
				return
			}
		}
	}
}

func completeReceive(t *testing.T, messages <-chan completeMail) completeMail {
	t.Helper()
	select {
	case msg := <-messages:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP receiver did not receive message")
		return completeMail{}
	}
}

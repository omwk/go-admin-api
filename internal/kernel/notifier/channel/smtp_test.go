package channel

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSMTPEncryptedDelivery(t *testing.T) {
	certificate := httptest.NewTLSServer(nil)
	defer certificate.Close()
	pool := x509.NewCertPool()
	pool.AddCert(certificate.Certificate())
	for _, mode := range []string{"tls", "starttls"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewUnstartedServer(nil)
			secured := &atomic.Bool{}
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := server.Listener.Accept()
				if err == nil {
					serveSMTP(t, conn, mode, certificate.TLS, secured)
				}
			}()
			t.Cleanup(func() { server.Close(); <-done })
			address := randomValue(t) + "@example.invalid"
			sender, err := NewSender("email", "smtp", Config{"smtp_addr": server.Listener.Addr().String(), "from_address": address, "tls_mode": mode, "test_recipient": address})
			require.NoError(t, err)
			sender.(*smtpSender).tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
			require.NoError(t, sender.Test(context.Background()))
			require.True(t, secured.Load())
		})
	}
}

func serveSMTP(t *testing.T, connection net.Conn, mode string, config *tls.Config, secured *atomic.Bool) {
	t.Helper()
	defer func() { require.NoError(t, connection.Close()) }()
	require.NoError(t, connection.SetDeadline(time.Now().Add(5*time.Second)))
	if mode == "tls" {
		connection = tls.Server(connection, config)
	}
	write := func(text string) { _, err := io.WriteString(connection, text); require.NoError(t, err) }
	write("220 local\r\n")
	reader := bufio.NewReader(connection)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			if _, tlsActive := connection.(*tls.Conn); tlsActive {
				write("250 local\r\n")
			} else {
				write("250-local\r\n250 STARTTLS\r\n")
			}
		case strings.HasPrefix(line, "STARTTLS"):
			write("220 upgrade\r\n")
			connection = tls.Server(connection, config)
			reader = bufio.NewReader(connection)
		case strings.HasPrefix(line, "DATA"):
			_, tlsActive := connection.(*tls.Conn)
			secured.Store(tlsActive)
			write("354 data\r\n")
			for {
				line, err = reader.ReadString('\n')
				require.NoError(t, err)
				if line == ".\r\n" {
					break
				}
			}
			write("250 accepted\r\n")
		case strings.HasPrefix(line, "QUIT"):
			write("221 bye\r\n")
			return
		default:
			write("250 ok\r\n")
		}
	}
}

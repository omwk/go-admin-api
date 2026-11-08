package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func serveTLS() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return err
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "S3 local fixture"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return err
	}
	backend, err := url.Parse(os.Getenv("SMOKE_BACKEND"))
	if err != nil || backend.Hostname() != "127.0.0.1" {
		return errors.New("local backend required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }() // Server.Serve also closes its listener.
	proxy := httputil.NewSingleHostReverseProxy(backend)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusBadGateway) }
	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 10 * time.Second}
	path := os.Getenv("SMOKE_ARTIFACTS")
	if err := os.WriteFile(filepath.Join(path, "tls.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return err
	}
	info, err := json.Marshal(map[string]string{"url": "https://" + listener.Addr().String()})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, "tls.json"), info, 0o600); err != nil {
		return err
	}
	tlsListener := tls.NewListener(listener, &tls.Config{MinVersion: tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	return server.Serve(tlsListener)
}

func requestHTTP(ctx context.Context) (result any, err error) {
	var input struct {
		Method, URL string
		Body        json.RawMessage
		Headers     map[string]string
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return nil, err
	}
	cert, err := os.ReadFile(filepath.Join(os.Getenv("SMOKE_ARTIFACTS"), "tls.pem"))
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		return nil, errors.New("smoke TLS certificate required")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, input.Method, input.URL, bytes.NewReader(input.Body))
	if err != nil {
		return nil, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	req.Header.Set("X-Request-ID", hex.EncodeToString(id[:]))
	for key, value := range input.Headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var envelope json.RawMessage
	if len(body) > 0 {
		if !json.Valid(body) {
			return nil, errors.New("expected JSON HTTP response")
		}
		envelope = body
	}
	return map[string]any{"status": resp.StatusCode, "request_id": resp.Header.Get("X-Request-ID"),
		"body": envelope, "cookies": resp.Cookies()}, nil
}

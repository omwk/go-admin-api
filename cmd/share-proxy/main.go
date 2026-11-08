// Command share-proxy publishes only the reviewed share API allowlist.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

func main() {
	if err := run(); err != nil {
		log.Fatal("share proxy stopped")
	}
}

func run() error {
	config := flag.String("config", "deploy/share-public.json", "reviewed public route configuration")
	listen := flag.String("listen", "127.0.0.1:8081", "listener behind the TLS terminator")
	upstream := flag.String("upstream", "http://127.0.0.1:8080", "private API upstream")
	trusted := flag.String("trusted-proxies", "", "comma-separated TLS proxy addresses; empty ignores forwarding headers")
	flag.Parse()
	target, err := url.Parse(*upstream)
	if err != nil || target.Scheme != "http" && target.Scheme != "https" || target.Host == "" || target.User != nil || target.RawQuery != "" {
		return errors.New("invalid private upstream")
	}
	file, err := os.Open(*config)
	if err != nil {
		return err
	}
	var proxies []string
	if *trusted != "" {
		for _, proxy := range strings.Split(*trusted, ",") {
			proxies = append(proxies, strings.TrimSpace(proxy))
		}
	}
	handler, loadErr := publicProxy(target, proxies, file)
	if err := errors.Join(loadErr, file.Close()); err != nil {
		return err
	}
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 31 * time.Second, IdleTimeout: time.Minute}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return httpx.Serve(ctx, server)
}

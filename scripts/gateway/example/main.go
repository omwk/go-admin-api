// Command example calls the M8 order status or usage API using the sample client.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/scripts/gateway/client"
)

func main() {
	if !run() {
		fmt.Fprintln(os.Stderr, "FAIL: open example request (check configuration, authorization and response code)")
		os.Exit(1)
	}
}

func run() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := os.Getenv("CH_OPEN_PATH")
	if path == "" {
		path = "/open/v1/orders/" + os.Getenv("CH_OPEN_ORDER_ID") + "/status"
	}
	endpoint := strings.TrimRight(os.Getenv("CH_OPEN_BASE_URL"), "/") + path
	r, err := client.NewRequest(ctx, "GET", endpoint, os.Getenv("CH_OPEN_APP_ID"), os.Getenv("CH_OPEN_SECRET"), nil)
	if err != nil {
		return false
	}
	httpClient := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := httpClient.Do(r)
	if err != nil {
		return false
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	closeErr := resp.Body.Close()
	var envelope struct{ Code int }
	if readErr != nil || closeErr != nil || json.Unmarshal(body, &envelope) != nil || resp.StatusCode != 200 || envelope.Code != 0 {
		return false
	}
	_, err = os.Stdout.Write(body)
	return err == nil
}

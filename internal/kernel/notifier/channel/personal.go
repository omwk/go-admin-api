package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// PersonalTransport sends to a validated user destination, without tenant credentials.
type PersonalTransport struct {
	AllowedHosts map[string][]string
	LookupIP     func(context.Context, string) ([]net.IPAddr, error)
	Transport    http.RoundTripper
}

// ErrPersonalDestinationChanged prevents a completed probe from verifying a replacement URL.
var ErrPersonalDestinationChanged = errors.New("personal destination changed")

// PersonalChannel identifies channels whose destination belongs to the user.
func PersonalChannel(kind string) bool {
	return kind == "webhook" || kind == "bark" || kind == "gotify"
}

// ProviderScope describes a transport's intended audience, independently of enablement.
func ProviderScope(kind string) string {
	if kind == "wecom" || kind == "dingtalk" {
		return "system"
	}
	return "personal"
}

// Validate rejects unsafe URLs and every non-public DNS answer before storage.
func (p *PersonalTransport) Validate(ctx context.Context, kind, raw string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u, err := personalURL(kind, raw)
	if err != nil {
		return err
	}
	hosts := p.AllowedHosts[kind]
	if len(hosts) > 0 && !slices.ContainsFunc(hosts, func(host string) bool { return strings.EqualFold(host, u.Hostname()) }) {
		return errors.New("个人渠道主机不在允许列表中")
	}
	_, err = p.publicIPs(ctx, u.Hostname())
	return err
}

func personalURL(kind, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if !PersonalChannel(kind) || len(raw) > 2048 || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\\") {
		return nil, errors.New("个人渠道地址必须是有效的 HTTPS URL，且不超过 2048 字节")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n <= 0 || n > 65535 {
			return nil, errors.New("个人渠道端口无效")
		}
	}
	return u, nil
}

func (p *PersonalTransport) publicIPs(ctx context.Context, host string) ([]net.IPAddr, error) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, errors.New("个人渠道地址必须使用公网主机")
	}
	lookup := p.LookupIP
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	var addresses []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		addresses = []net.IPAddr{{IP: ip}}
	} else {
		var err error
		addresses, err = lookup(ctx, host)
		if err != nil {
			return nil, errors.New("个人渠道主机解析失败")
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("个人渠道主机解析失败")
	}
	for _, address := range addresses {
		if address.Zone != "" || !publicIP(address.IP) {
			return nil, errors.New("个人渠道地址必须使用公网主机")
		}
	}
	return addresses, nil
}

func publicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if address.Is6() && !netip.MustParsePrefix("2000::/3").Contains(address) {
		return false
	}
	for _, prefix := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16"} {
		if netip.MustParsePrefix(prefix).Contains(address) {
			return false
		}
	}
	return true
}

func (p *PersonalTransport) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("个人渠道连接失败")
	}
	addresses, err := p.publicIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	for _, ip := range addresses {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("个人渠道连接失败")
}

// Send posts only title/body; redirects, proxies and private network destinations are blocked.
func (p *PersonalTransport) Send(ctx context.Context, kind, endpoint string, m Message) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := p.Validate(ctx, kind, endpoint); err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]string{"title": m.Subject, "body": m.Body})
	if err != nil {
		return errors.New("个人渠道消息编码失败")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return errors.New("个人渠道请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	transport := p.Transport
	if transport == nil {
		t := &http.Transport{DialContext: p.dial, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, DisableKeepAlives: true}
		defer t.CloseIdleConnections()
		transport = t
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("个人渠道投递失败")
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 65537))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("个人渠道投递失败")
	}
	return nil
}

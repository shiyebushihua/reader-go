package engine

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

const maxResponse = 8 << 20

var fetchSlots = make(chan struct{}, 64)

// Engine has no database state. Its private client is replaceable only by package tests.
type Engine struct{ client *http.Client }

func New() *Engine {
	tr := &http.Transport{Proxy: nil, DisableCompression: true, MaxIdleConns: 128, MaxIdleConnsPerHost: 16, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second, MaxResponseHeaderBytes: 64 << 10}
	tr.DialContext = publicDial
	return &Engine{client: &http.Client{Transport: tr, Timeout: 12 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("redirect limit exceeded")
		}
		if err := validURL(req.URL); err != nil {
			return err
		}
		// Never forward source credentials across origins, including HTTPS downgrades.
		if len(via) > 0 && (req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme) {
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			req.Header.Del("Proxy-Authorization")
		}
		return nil
	}}}
}

var blockedNetworks = func() []netip.Prefix {
	cidrs := []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "::/96", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10", "ff00::/8"}
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, s := range cidrs {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range blockedNetworks {
		if p.Contains(ip) {
			return false
		}
	}
	return ip.Zone() == ""
}
func validURL(u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("only absolute HTTP(S) URLs without credentials are allowed")
	}
	if strings.Contains(u.Hostname(), "%") {
		return fmt.Errorf("IPv6 zones are not allowed")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicIP(ip) {
		return fmt.Errorf("blocked non-public destination")
	}
	return nil
}
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	return dialPublic(ctx, network, address, net.DefaultResolver.LookupNetIP,
		(&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}).DialContext)
}

func dialPublic(ctx context.Context, network, address string,
	lookup func(context.Context, string, string) ([]netip.Addr, error),
	dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := lookup(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve destination: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("destination has no addresses")
	}
	// Fail closed on mixed public/private answers. Dial a validated literal, never resolve twice.
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, fmt.Errorf("blocked non-public destination")
		}
	}
	var last error
	for _, ip := range ips {
		c, e := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return c, nil
		}
		last = e
	}
	return nil, last
}

type requestSpec struct {
	URL     string
	Method  string
	Headers map[string]string
	Body    string
	Charset string
}
type fetched struct {
	Body []byte
	URL  string
}

func (e *Engine) fetch(ctx context.Context, spec requestSpec) (fetched, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	select {
	case fetchSlots <- struct{}{}:
		defer func() { <-fetchSlots }()
	case <-ctx.Done():
		return fetched{}, ctx.Err()
	}
	u, err := url.Parse(spec.URL)
	if err != nil {
		return fetched{}, err
	}
	if err = validURL(u); err != nil {
		return fetched{}, err
	}
	method := strings.ToUpper(spec.Method)
	if method == "" {
		method = "GET"
	}
	if method != "GET" && method != "POST" {
		return fetched{}, fmt.Errorf("unsupported HTTP method %q", method)
	}
	if len(spec.Body) > 1<<20 {
		return fetched{}, fmt.Errorf("request body exceeds 1 MiB")
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(spec.Body))
	if err != nil {
		return fetched{}, err
	}
	req.Header.Set("User-Agent", "ReaderGo/1.0")
	req.Header.Set("Accept-Encoding", "gzip")
	for k, v := range spec.Headers {
		if strings.EqualFold(k, "Host") || strings.EqualFold(k, "Proxy-Authorization") || strings.EqualFold(k, "Accept-Encoding") {
			continue
		}
		req.Header.Set(k, v)
	}
	client := e.client
	if client == nil {
		client = New().client
	}
	resp, err := client.Do(req)
	if err != nil {
		return fetched{}, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fetched{}, fmt.Errorf("upstream HTTP status %d", resp.StatusCode)
	}
	raw := &io.LimitedReader{R: resp.Body, N: maxResponse + 1}
	var r io.Reader = raw
	switch strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		gz, er := gzip.NewReader(raw)
		if er != nil {
			return fetched{}, er
		}
		defer gz.Close()
		r = gz
	default:
		return fetched{}, fmt.Errorf("unsupported response content encoding")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxResponse+1))
	if err != nil {
		return fetched{}, err
	}
	if len(data) > maxResponse || raw.N <= 0 {
		return fetched{}, fmt.Errorf("response exceeds 8 MiB limit")
	}
	label := spec.Charset
	if label == "" {
		_, params, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		label = params["charset"]
	}
	data, err = decodeCharset(data, label, resp.Header.Get("Content-Type"))
	if err != nil {
		return fetched{}, err
	}
	if len(data) > maxResponse {
		return fetched{}, fmt.Errorf("decoded response exceeds 8 MiB limit")
	}
	final := u.String()
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return fetched{data, final}, nil
}
func decodeCharset(data []byte, label, contentType string) ([]byte, error) {
	label = strings.ToLower(strings.TrimSpace(label))
	var r io.Reader
	switch label {
	case "utf-8", "utf8":
		return data, nil
	case "gbk", "gb2312", "gb18030":
		r = transform.NewReader(strings.NewReader(string(data)), simplifiedchinese.GB18030.NewDecoder())
	case "big5":
		r = transform.NewReader(strings.NewReader(string(data)), traditionalchinese.Big5.NewDecoder())
	case "":
		// JSON and most modern pages default to UTF-8; honor an HTML meta declaration.
		if strings.Contains(strings.ToLower(contentType), "html") {
			enc, _, _ := charset.DetermineEncoding(data, contentType)
			if strings.Contains(strings.ToLower(string(data[:min(len(data), 1024)])), "charset") {
				r = enc.NewDecoder().Reader(strings.NewReader(string(data)))
			}
		}
		if r == nil {
			return data, nil
		}
	default:
		enc, _ := charset.Lookup(label)
		if enc == nil {
			return nil, fmt.Errorf("unsupported charset %q", label)
		}
		r = enc.NewDecoder().Reader(strings.NewReader(string(data)))
	}
	out, err := io.ReadAll(io.LimitReader(r, maxResponse+1))
	return out, err
}

func (e *Engine) FetchSource(ctx context.Context, rawURL string) (json.RawMessage, error) {
	p, err := e.fetch(ctx, requestSpec{URL: rawURL})
	if err != nil {
		return nil, err
	}
	data := []byte(strings.TrimPrefix(strings.TrimSpace(string(p.Body)), "\ufeff"))
	if !json.Valid(data) || len(data) == 0 || (data[0] != '{' && data[0] != '[') {
		return nil, fmt.Errorf("source response must be a JSON object or array")
	}
	return json.RawMessage(data), nil
}

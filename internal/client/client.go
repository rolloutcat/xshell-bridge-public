package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
}
type Client struct {
	Config
	HTTP *http.Client
}

func Load(path string) (*Client, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var c Config
	if e = json.Unmarshal(b, &c); e != nil {
		return nil, e
	}
	return New(c)
}
func New(c Config) (*Client, error) {
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return nil, fmt.Errorf("invalid bridge endpoint")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return nil, fmt.Errorf("HTTP is only allowed on numeric loopback; use SSH forwarding")
	}
	if len(c.Token) < 32 {
		return nil, fmt.Errorf("invalid pairing token")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Client{c, &http.Client{Timeout: 8 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirect refused") }}}, nil
}
func (c *Client) Call(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Endpoint, "/")+path, body)
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	r.Header.Set("Content-Type", "application/json")
	resp, e := c.HTTP.Do(r)
	if e != nil {
		return fmt.Errorf("bridge request failed: %w", e)
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if e != nil {
		return e
	}
	if resp.StatusCode != 200 {
		var v struct{ Code, Message string }
		if json.Unmarshal(b, &v) == nil && v.Code != "" {
			return fmt.Errorf("%s: %s", v.Code, v.Message)
		}
		return fmt.Errorf("bridge returned HTTP %d", resp.StatusCode)
	}
	if output != nil {
		return json.Unmarshal(b, output)
	}
	return nil
}

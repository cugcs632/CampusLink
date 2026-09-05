// Package portal implements the account API served by nap.cug.edu.cn since 2026.
package portal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultHost                   = "nap.cug.edu.cn"
	ExpectedGatewayIP             = "192.168.167.72"
	DefaultBaseURL                = "https://" + DefaultHost
	DefaultMaxResponseBytes int64 = 1 << 20
)

type Config struct {
	BaseURL          string
	Warn             func(string) // Receives advisory DNS messages, if set.
	NASID            string       // Empty means discover from the gateway redirect.
	ISP              string       // Empty uses the campus network.
	Timeout          time.Duration
	MaxResponseBytes int64
}

func DefaultConfig() Config {
	return Config{BaseURL: DefaultBaseURL, Timeout: 8 * time.Second, MaxResponseBytes: DefaultMaxResponseBytes}
}

type Client struct {
	config   Config
	baseURL  *url.URL
	http     *http.Client
	lookupIP func(context.Context, string) ([]net.IPAddr, error)
}

func NewClient(config Config) (*Client, error) {
	base := strings.TrimSpace(config.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, errors.New("invalid portal base URL")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, errors.New("portal base URL must use http or https and include a host")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("portal base URL must not contain user info, query parameters, or a fragment")
	}
	if strings.EqualFold(u.Hostname(), DefaultHost) && u.Port() == "" {
		u.Host = DefaultHost
		u.Scheme = "https"
	}
	if config.NASID != "" && !validNASID(config.NASID) {
		return nil, errors.New("NAS ID must be a positive integer")
	}
	if config.Timeout == 0 {
		config.Timeout = 8 * time.Second
	}
	if config.Timeout < 0 {
		return nil, errors.New("timeout must be greater than zero")
	}
	if config.MaxResponseBytes == 0 {
		config.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if config.MaxResponseBytes < 0 || config.MaxResponseBytes > 16<<20 {
		return nil, errors.New("max response size must be between 1 and 16777216 bytes")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		if req.URL.Hostname() == DefaultHost {
			return nil, nil
		}
		return http.ProxyFromEnvironment(req)
	}
	client := &Client{config: config, baseURL: u, lookupIP: net.DefaultResolver.LookupIPAddr}
	client.http = &http.Client{Jar: jar, Transport: transport, Timeout: config.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many portal redirects")
			}
			if req.URL.Host != u.Host || req.URL.User != nil {
				return errors.New("portal redirect changed host")
			}
			// /api/r/default currently emits an HTTP URL even when called over HTTPS.
			// Upgrade that redirect before sending any request or CSRF header.
			if via[0].URL.Scheme == "https" && req.URL.Scheme == "http" {
				req.URL.Scheme = "https"
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("invalid portal redirect scheme")
			}
			return nil
		},
	}
	return client, nil
}

func (c *Client) Login(username, password, ip string) (map[string]any, error) {
	return c.LoginContext(context.Background(), username, password, ip)
}

// The expected IP is advisory only. The HTTP transport resolves and connects to
// the configured hostname normally; it never substitutes the expected address.
func (c *Client) checkDNS(ctx context.Context) error {
	if !strings.EqualFold(c.baseURL.Hostname(), DefaultHost) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	addresses, err := c.lookupIP(ctx, DefaultHost)
	if err != nil {
		return fmt.Errorf("cannot resolve %s; check campus network DNS or VPN settings: %w", DefaultHost, err)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("DNS returned no addresses for %s; check campus network DNS settings", DefaultHost)
	}
	values := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if address.IP.Equal(net.ParseIP(ExpectedGatewayIP)) {
			return nil
		}
		values = append(values, address.String())
	}
	if c.config.Warn != nil {
		c.config.Warn(fmt.Sprintf("%s resolves to %s; expected %s. Check campus network DNS or VPN settings, or whether the gateway has changed. Continuing with the domain and HTTPS certificate verification.", DefaultHost, strings.Join(values, ", "), ExpectedGatewayIP))
	}
	return nil
}

func (c *Client) LoginContext(ctx context.Context, username, password, ip string) (map[string]any, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if username == "" {
		return nil, errors.New("username is required")
	}
	if password == "" {
		return nil, errors.New("password is required")
	}
	if ip != "" && net.ParseIP(ip) == nil {
		return nil, errors.New("invalid client IP")
	}
	params, token, status, err := c.session(ctx, ip)
	if err != nil {
		return nil, err
	}
	if OK(status) {
		return status, nil
	}
	if code, ok := status["code"].(float64); !ok || code != 1 {
		return status, nil
	}
	params.Set("username", username)
	params.Set("password", password)
	if c.config.ISP != "" {
		params.Set("isp", c.config.ISP)
	}
	check, err := c.requestJSON(ctx, http.MethodPost, "/api/account/check", params, token)
	if err != nil {
		return nil, err
	}
	if !OK(check) {
		return check, nil
	}
	return c.requestJSON(ctx, http.MethodPost, "/api/account/login", params, token)
}

// StatusContext queries the gateway without submitting credentials.
func (c *Client) StatusContext(ctx context.Context, ip string) (map[string]any, error) {
	_, _, status, err := c.session(ctx, ip)
	return status, err
}

func (c *Client) session(ctx context.Context, ip string) (url.Values, string, map[string]any, error) {
	if ctx == nil {
		return nil, "", nil, errors.New("context is required")
	}
	if ip != "" && net.ParseIP(ip) == nil {
		return nil, "", nil, errors.New("invalid client IP")
	}
	if err := c.checkDNS(ctx); err != nil {
		return nil, "", nil, err
	}
	params, err := c.discover(ctx, ip)
	if err != nil {
		return nil, "", nil, err
	}
	csrf, err := c.requestJSON(ctx, http.MethodGet, "/api/csrf-token", nil, "")
	if err != nil {
		return nil, "", nil, err
	}
	token, _ := csrf["csrf_token"].(string)
	if token == "" {
		return nil, "", nil, errors.New("CSRF token missing from portal response")
	}
	status, err := c.requestJSON(ctx, http.MethodGet, "/api/account/status", params, token)
	return params, token, status, err
}

func (c *Client) discover(ctx context.Context, ip string) (url.Values, error) {
	params := make(url.Values)
	if ip == "" || c.config.NASID == "" {
		_, finalURL, err := c.request(ctx, http.MethodGet, "/api/r/default", nil, "")
		if err != nil {
			return nil, err
		}
		query := finalURL.Query()
		params.Set("userIpv4", query.Get("wlanuserip"))
		if params.Get("userIpv4") == "" {
			params.Set("userIpv4", query.Get("ip"))
		}
		params.Set("nasId", query.Get("nasId"))
		for source, target := range map[string]string{"switchip": "switchip", "mac": "userMac"} {
			if value := query.Get(source); value != "" {
				params.Set(target, value)
			}
		}
	}
	if ip != "" {
		params.Set("userIpv4", ip)
	}
	if c.config.NASID != "" {
		params.Set("nasId", c.config.NASID)
	}
	if net.ParseIP(params.Get("userIpv4")) == nil {
		return nil, errors.New("cannot find valid client IP from portal redirect; pass --ip")
	}
	if !validNASID(params.Get("nasId")) {
		return nil, errors.New("cannot find valid NAS ID from portal redirect; pass --nas-id")
	}
	return params, nil
}

func validNASID(value string) bool {
	id, err := strconv.ParseUint(value, 10, 32)
	return err == nil && id > 0
}

func (c *Client) requestJSON(ctx context.Context, method, path string, params url.Values, token string) (map[string]any, error) {
	body, _, err := c.request(ctx, method, path, params, token)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil || result == nil {
		return nil, fmt.Errorf("portal request %s returned invalid JSON object", path)
	}
	return result, nil
}

func (c *Client) request(ctx context.Context, method, path string, params url.Values, token string) ([]byte, *url.URL, error) {
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	var body io.Reader
	if method == http.MethodGet {
		u.RawQuery = params.Encode()
	} else {
		body = strings.NewReader(params.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, nil, fmt.Errorf("create portal request for %s failed", path)
	}
	req.Header.Set("User-Agent", "CampusLink/2")
	req.Header.Set("Accept", "application/json, text/html;q=0.9")
	if token != "" {
		req.Header.Set("X-CSRF-Token", token)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		req.Header.Set("Origin", c.baseURL.Scheme+"://"+c.baseURL.Host)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, nil, fmt.Errorf("portal request %s failed: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("portal request %s returned HTTP %d", path, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.config.MaxResponseBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read portal response: %w", err)
	}
	if int64(len(data)) > c.config.MaxResponseBytes {
		return nil, nil, fmt.Errorf("portal response exceeds %d bytes", c.config.MaxResponseBytes)
	}
	return data, resp.Request.URL, nil
}

func OK(result map[string]any) bool {
	code, ok := result["code"].(float64)
	return ok && code == 0 && !PasswordChangeRequired(result)
}

func PasswordChangeRequired(result map[string]any) bool {
	return result["isChangePwd"] == float64(1) || result["isChangePwd"] == "1"
}

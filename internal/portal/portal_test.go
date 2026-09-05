package portal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoginFlow(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/r/default":
			http.Redirect(w, r, "/api/r/7", http.StatusFound)
		case "/api/r/7":
			http.Redirect(w, r, "/tpl/cug/login.html?ip=10.0.0.2&nasId=7&switchip=10.0.0.1&mac=aa%3Abb%3Acc%3Add%3Aee%3Aff", http.StatusFound)
		case "/tpl/cug/login.html":
			fmt.Fprint(w, "<html>account login</html>")
		case "/api/csrf-token":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "test-session", Path: "/"})
			fmt.Fprint(w, `{"csrf_token":"test-token"}`)
		case "/api/account/status", "/api/account/check", "/api/account/login":
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "test-session" || r.Header.Get("X-CSRF-Token") != "test-token" {
				t.Error("missing CSRF token or session cookie")
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			for key, want := range map[string]string{"userIpv4": "10.0.0.2", "nasId": "7", "switchip": "10.0.0.1", "userMac": "aa:bb:cc:dd:ee:ff"} {
				if r.Form.Get(key) != want {
					t.Errorf("%s = %q, want %q", key, r.Form.Get(key), want)
				}
			}
			if r.URL.Path == "/api/account/status" {
				if r.Method != http.MethodGet || r.Form.Has("password") || r.Form.Has("username") {
					t.Error("status request contained credentials or used POST")
				}
				fmt.Fprint(w, `{"code":1,"msg":"离线"}`)
				return
			}
			if r.Method != http.MethodPost || r.URL.RawQuery != "" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
				t.Error("credentials must be sent in a form POST")
			}
			if r.PostForm.Get("username") != "user+中文" || r.PostForm.Get("password") != " pass&+=中文 " {
				t.Error("form credentials did not survive encoding")
			}
			if r.URL.Path == "/api/account/check" {
				fmt.Fprint(w, `{"code":0,"isChangePwd":0}`)
			} else {
				fmt.Fprint(w, `{"code":0,"msg":"认证成功"}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testClient(t, server.URL)
	result, err := client.Login("user+中文", " pass&+=中文 ", "")
	if err != nil || !OK(result) {
		t.Fatalf("result=%v err=%v", result, err)
	}
	want := []string{"/api/r/default", "/api/r/7", "/tpl/cug/login.html", "/api/csrf-token", "/api/account/status", "/api/account/check", "/api/account/login"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("requests = %v", paths)
	}
}

func TestLoginStopsAtOnlineOrRejectedResponse(t *testing.T) {
	cases := []struct {
		name, status, check, login, last string
		success                          bool
	}{
		{"online", `{"code":0,"msg":"在线"}`, "", "", "status", true},
		{"status error", `{"code":3,"msg":"unavailable"}`, "", "", "status", false},
		{"status missing code", `{}`, "", "", "status", false},
		{"wrong password", `{"code":1}`, `{"code":1,"msg":"密码错误"}`, "", "check", false},
		{"password change", `{"code":1}`, `{"code":0,"isChangePwd":1}`, "", "check", false},
		{"captcha at check", `{"code":1}`, `{"code":2}`, "", "check", false},
		{"captcha at login", `{"code":1}`, `{"code":0}`, `{"code":2,"captcha":{"captchaId":"test"}}`, "login", false},
		{"login rejected", `{"code":1}`, `{"code":0}`, `{"code":1,"msg":"认证失败"}`, "login", false},
		{"login accepted", `{"code":1}`, `{"code":0}`, `{"code":0}`, "login", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			last := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				last = strings.TrimPrefix(r.URL.Path, "/api/account/")
				switch r.URL.Path {
				case "/api/csrf-token":
					fmt.Fprint(w, `{"csrf_token":"test"}`)
				case "/api/account/status":
					fmt.Fprint(w, tc.status)
				case "/api/account/check":
					fmt.Fprint(w, tc.check)
				case "/api/account/login":
					fmt.Fprint(w, tc.login)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			client := testClient(t, server.URL)
			client.config.NASID = "9"
			result, err := client.Login("user", "pass", "10.0.0.8")
			if err != nil || OK(result) != tc.success || last != tc.last {
				t.Fatalf("result=%v err=%v last=%s", result, err, last)
			}
		})
	}
}

func TestDiscoveryOverrides(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/r/default" {
			http.Redirect(w, r, "/login?ip=10.0.0.2&wlanuserip=10.0.0.3&nasId=7", http.StatusFound)
			return
		}
		fmt.Fprint(w, "login")
	}))
	defer server.Close()
	client := testClient(t, server.URL)
	params, err := client.discover(context.Background(), "")
	if err != nil || params.Get("userIpv4") != "10.0.0.3" || params.Get("nasId") != "7" {
		t.Fatalf("params=%v err=%v", params, err)
	}
	params, err = client.discover(context.Background(), "10.0.0.4")
	if err != nil || params.Get("userIpv4") != "10.0.0.4" || params.Get("nasId") != "7" {
		t.Fatalf("params=%v err=%v", params, err)
	}
	client.config.NASID = "12"
	params, err = client.discover(context.Background(), "")
	if err != nil || params.Get("userIpv4") != "10.0.0.3" || params.Get("nasId") != "12" {
		t.Fatalf("params=%v err=%v", params, err)
	}
}

func TestBadDiscovery(t *testing.T) {
	for _, query := range []string{"", "ip=not-an-ip&nasId=7", "ip=10.0.0.2", "ip=10.0.0.2&nasId=0"} {
		t.Run(query, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/r/default" {
					http.Redirect(w, r, "/login?"+query, http.StatusFound)
					return
				}
				fmt.Fprint(w, "login")
			}))
			defer server.Close()
			if _, err := testClient(t, server.URL).discover(context.Background(), ""); err == nil {
				t.Fatal("expected discovery error")
			}
		})
	}
}

func TestRequestFailures(t *testing.T) {
	for _, scenario := range []string{"oversize", "http", "json", "csrf", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "oversize":
					fmt.Fprint(w, strings.Repeat("sensitive", 30))
				case "http":
					http.Error(w, "sensitive", 500)
				case "json":
					fmt.Fprint(w, "sensitive invalid JSON")
				case "csrf":
					fmt.Fprint(w, `{}`)
				case "timeout":
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			client := testClient(t, server.URL)
			client.config.NASID = "7"
			client.config.MaxResponseBytes = 64
			client.http.Timeout = 50 * time.Millisecond
			_, err := client.Login("sensitive-user", "sensitive-password", "10.0.0.2")
			if err == nil || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRedirectSafety(t *testing.T) {
	client := testClient(t, "https://portal.example")
	initial, _ := http.NewRequest(http.MethodGet, "https://portal.example/api/r/default", nil)
	redirect, _ := http.NewRequest(http.MethodGet, "http://portal.example/login?ip=10.0.0.2&nasId=7", nil)
	if err := client.http.CheckRedirect(redirect, []*http.Request{initial}); err != nil || redirect.URL.Scheme != "https" {
		t.Fatalf("HTTPS redirect: %v %v", redirect.URL, err)
	}
	for _, target := range []string{"https://other.example/login", "https://user:pass@portal.example/login", "ftp://portal.example/login"} {
		redirect.URL, _ = url.Parse(target)
		if err := client.http.CheckRedirect(redirect, []*http.Request{initial}); err == nil {
			t.Errorf("allowed unsafe redirect %s", target)
		}
	}
	var received bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received = true }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := testClient(t, server.URL).requestJSON(context.Background(), http.MethodPost, "/api/account/login", url.Values{"password": {"secret"}}, "token")
	if err == nil || received || strings.Contains(err.Error(), "secret") {
		t.Fatalf("redirect leaked credentials: received=%v err=%v", received, err)
	}
}

func TestConfigAndInput(t *testing.T) {
	for _, config := range []Config{{BaseURL: "ftp://portal.example"}, {BaseURL: "http://user:pass@portal.example"}, {BaseURL: "http://portal.example?secret=x"}, {NASID: "-1"}, {Timeout: -time.Second}, {MaxResponseBytes: 17 << 20}} {
		if _, err := NewClient(config); err == nil {
			t.Fatalf("accepted config: %#v", config)
		}
	}
	for _, base := range []string{"", DefaultHost, "http://" + DefaultHost} {
		client := testClient(t, base)
		if client.baseURL.String() != DefaultBaseURL {
			t.Fatalf("incorrect defaults for %q: %v", base, client.baseURL)
		}
	}
	client := testClient(t, "")
	for _, input := range [][3]string{{"", "pass", ""}, {"user", "", ""}, {"user", "pass", "invalid"}} {
		if _, err := client.Login(input[0], input[1], input[2]); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
	if _, err := client.LoginContext(nil, "user", "pass", ""); err == nil {
		t.Fatal("accepted nil context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.LoginContext(ctx, "user", "pass", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	for _, result := range []map[string]any{nil, {}, {"code": "0"}, {"code": false}, {"code": float64(1)}, {"code": float64(0), "isChangePwd": "1"}} {
		if OK(result) {
			t.Fatalf("false success: %v", result)
		}
	}
}

func testClient(t *testing.T, base string) *Client {
	t.Helper()
	config := DefaultConfig()
	config.BaseURL = base
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.http.CloseIdleConnections)
	return client
}

func TestDNSCheckAndDomainConnection(t *testing.T) {
	for _, tc := range []struct {
		name       string
		addresses  []string
		lookupErr  error
		warn, fail bool
	}{
		{name: "expected", addresses: []string{ExpectedGatewayIP}},
		{name: "multiple answers", addresses: []string{"2001:db8::1", ExpectedGatewayIP}},
		{name: "changed IPv4", addresses: []string{"192.0.2.1"}, warn: true},
		{name: "IPv6 only", addresses: []string{"2001:db8::1"}, warn: true},
		{name: "empty DNS response", fail: true},
		{name: "DNS failed", lookupErr: errors.New("DNS unavailable"), fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, "")
			client.config.NASID = "7"
			var warnings []string
			client.config.Warn = func(message string) { warnings = append(warnings, message) }
			lookups := 0
			client.lookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
				lookups++
				if host != DefaultHost {
					t.Errorf("DNS hostname = %q", host)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Error("DNS lookup missing timeout")
				}
				var addresses []net.IPAddr
				for _, value := range tc.addresses {
					addresses = append(addresses, net.IPAddr{IP: net.ParseIP(value)})
				}
				return addresses, tc.lookupErr
			}
			requests := 0
			client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.Host != DefaultHost || r.URL.Scheme != "https" {
					t.Errorf("request no longer uses domain HTTPS: %v", r.URL)
				}
				body := `{"code":0,"msg":"在线"}`
				if r.URL.Path == "/api/csrf-token" {
					body = `{"csrf_token":"test"}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Request: r, Header: make(http.Header)}, nil
			})
			result, err := client.Login("user", "pass", "10.0.0.2")
			if lookups != 1 || (err != nil) != tc.fail {
				t.Fatalf("lookups=%d result=%v err=%v", lookups, result, err)
			}
			if tc.fail && requests != 0 {
				t.Fatal("sent requests after DNS failure")
			}
			if !tc.fail && (!OK(result) || requests != 2) {
				t.Fatalf("login did not continue: %v, requests=%d", result, requests)
			}
			if (len(warnings) == 1) != tc.warn {
				t.Fatalf("warnings=%v", warnings)
			}
			if tc.warn && (!strings.Contains(warnings[0], ExpectedGatewayIP) || !strings.Contains(warnings[0], tc.addresses[0])) {
				t.Fatalf("warning lacks DNS details: %v", warnings)
			}
		})
	}
}

func TestDNSCheckHonorsTimeoutAndCustomHost(t *testing.T) {
	client := testClient(t, "")
	client.config.Timeout = 20 * time.Millisecond
	client.lookupIP = func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := client.checkDNS(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DNS timeout: %v", err)
	}
	client.baseURL, _ = url.Parse("https://portal.example")
	client.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		t.Fatal("custom host should skip campus DNS check")
		return nil, nil
	}
	if err := client.checkDNS(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

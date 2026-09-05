package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cugcs632/CampusLink/internal/autostart"
	"github.com/cugcs632/CampusLink/internal/profile"
)

func testApp(t *testing.T) (*application, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	store, _ := profile.Open(t.TempDir())
	var out, errOut bytes.Buffer
	a := &application{store: store, stdin: strings.NewReader(""), stdout: &out, stderr: &errOut}
	a.manager = func() (*autostart.Manager, error) {
		return &autostart.Manager{OS: "darwin", Home: t.TempDir(), UserID: "1000", Store: store, Run: func(string, ...string) (string, error) { return "", nil }}, nil
	}
	return a, &out, &errOut
}

func saveTestProfile(t *testing.T, a *application, base string) {
	t.Helper()
	if err := a.store.Save(profile.Config{Username: "saved-user", BaseURL: base, NASID: "7", IP: "10.0.0.2", Storage: "file", Timeout: 8}, "saved-password"); err != nil {
		t.Fatal(err)
	}
}

func TestSavedCredentialsAndOverrides(t *testing.T) {
	clearConfigEnv(t)
	for _, tc := range []struct {
		name           string
		args           []string
		user, password string
		fail           bool
	}{
		{name: "saved", user: "saved-user", password: "saved-password"},
		{name: "override", args: []string{"-u", "temporary-user", "-p", "temporary-password"}, user: "temporary-user", password: "temporary-password"},
		{name: "empty flag", args: []string{"--password", ""}, fail: true},
		{name: "other account", args: []string{"--username", "other-user"}, fail: true},
		{name: "other gateway", args: []string{"--base-url", "https://other.example"}, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, out, errOut := testApp(t)
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/csrf-token":
					fmt.Fprint(w, `{"csrf_token":"test"}`)
				case "/api/account/status":
					fmt.Fprint(w, `{"code":1}`)
				default:
					posts++
					_ = r.ParseForm()
					if r.PostForm.Get("username") != tc.user || r.PostForm.Get("password") != tc.password {
						t.Error("incorrect credentials")
					}
					fmt.Fprint(w, `{"code":0}`)
				}
			}))
			defer server.Close()
			saveTestProfile(t, a, server.URL)
			code := a.login(tc.args)
			if tc.fail {
				if code == 0 || posts != 0 {
					t.Fatalf("invalid override submitted credentials: code=%d posts=%d", code, posts)
				}
			} else if code != 0 || out.String() != "login ok\n" {
				t.Fatalf("code=%d err=%s", code, errOut)
			}
		})
	}
}

func TestBackgroundPausesUntilSuccessfulLogin(t *testing.T) {
	clearConfigEnv(t)
	a, _, errOut := testApp(t)
	calls := 0
	accepted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/api/csrf-token":
			fmt.Fprint(w, `{"csrf_token":"test"}`)
		case "/api/account/status":
			fmt.Fprint(w, `{"code":1}`)
		default:
			if accepted {
				fmt.Fprint(w, `{"code":0}`)
			} else {
				fmt.Fprint(w, `{"code":2,"msg":"sensitive-server-message","captcha":{"captchaId":"secret"}}`)
			}
		}
	}))
	defer server.Close()
	saveTestProfile(t, a, server.URL)
	t.Setenv("CAMPUSLINK_USERNAME", "ignored-env-user")
	t.Setenv("CAMPUSLINK_PASSWORD", "ignored-env-password")
	if code := a.background(); code != 1 {
		t.Fatalf("background code=%d", code)
	}
	state, _ := a.store.ReadState()
	if state.Paused == "" {
		t.Fatal("verification did not pause retries")
	}
	before := calls
	if code := a.background(); code != 0 || before != calls {
		t.Fatal("paused background made requests")
	}
	logs, _ := a.store.Logs()
	for _, secret := range []string{"saved-password", "sensitive-server-message", "secret", "ignored-env-password"} {
		if strings.Contains(string(logs), secret) {
			t.Fatal("log exposed sensitive data")
		}
	}
	accepted = true
	if code := a.login([]string{"-u", "saved-user", "-p", "saved-password"}); code != 0 {
		t.Fatalf("manual login failed: %s", errOut)
	}
	state, _ = a.store.ReadState()
	if state.Paused != "" || state.LastSuccess.IsZero() {
		t.Fatal("successful login did not resume retries")
	}
}

func TestStatusIsReadOnlyAndJSONHasNoPassword(t *testing.T) {
	clearConfigEnv(t)
	a, out, errOut := testApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || strings.Contains(r.URL.RawQuery, "password") {
			t.Fatal("diagnostic submitted credentials")
		}
		if r.URL.Path == "/api/csrf-token" {
			fmt.Fprint(w, `{"csrf_token":"test"}`)
		} else {
			fmt.Fprint(w, `{"code":0,"online":{"Password":"must-not-display"}}`)
		}
	}))
	defer server.Close()
	saveTestProfile(t, a, server.URL)
	if code := a.inspect([]string{"--json"}, true); code != 0 {
		t.Fatalf("doctor failed: %s", errOut)
	}
	var report map[string]any
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report["online"] != true || report["saved_credentials_accessible"] != true {
		t.Fatalf("report=%v", report)
	}
	if strings.Contains(out.String(), "must-not-display") || strings.Contains(out.String(), "saved-password") {
		t.Fatal("diagnostics leaked credentials")
	}
}

func TestSetupAndCommandDispatch(t *testing.T) {
	clearConfigEnv(t)
	a, _, errOut := testApp(t)
	server := mockPortal(t, `{"code":0}`)
	defer server.Close()
	a.stdin = strings.NewReader("setup-password\n")
	if code := a.setup([]string{"--username", "setup-user", "--base-url", server.URL, "--storage", "file", "--password-stdin", "--no-autostart"}); code != 0 {
		t.Fatalf("setup failed: %s", errOut)
	}
	c, err := a.store.Load()
	if err != nil || c.Username != "setup-user" {
		t.Fatalf("saved config=%v err=%v", c, err)
	}
	password, err := a.store.Password(c)
	if err != nil || password != "setup-password" {
		t.Fatal("setup did not save password")
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"--config-dir", a.store.Dir, "login"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatalf("dispatch code=%d stderr=%s", code, &stderr)
	}
	for _, args := range [][]string{{"help"}, {"login", "--help"}, {"setup", "--help"}, {"status", "--help"}, {"doctor", "--help"}, {"logs"}, {"--version"}} {
		if code := run(args, strings.NewReader(""), &out, &stderr); code != 0 {
			t.Fatalf("%v failed: %s", args, &stderr)
		}
	}
	if err := os.WriteFile(filepath.Join(a.store.Dir, "config.json"), []byte("invalid config"), 0600); err != nil {
		t.Fatal(err)
	}
	a.stdin = strings.NewReader("replacement-password\n")
	if code := a.setup([]string{"--username", "setup-user", "--base-url", server.URL, "--storage", "file", "--password-stdin", "--no-autostart"}); code != 0 {
		t.Fatalf("setup could not repair config: %s", errOut)
	}
}

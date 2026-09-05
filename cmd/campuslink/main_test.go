package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONFailureReturnsNonzero(t *testing.T) {
	clearConfigEnv(t)
	server := mockPortal(t, `{"code":1,"msg":"password_error"}`)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{
		"--base-url", server.URL,
		"--username", "user",
		"--ip", "10.0.0.2",
		"--password-stdin",
		"--json",
	}, strings.NewReader("pass\n"), &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "password_error") {
		t.Fatalf("JSON response missing from stdout: %q", stdout.String())
	}
}

func TestSuccessfulLogin(t *testing.T) {
	clearConfigEnv(t)
	server := mockPortal(t, `{"code":0,"msg":"认证成功"}`)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{
		"--base-url", server.URL,
		"-u", "user",
		"-p", "pass",
		"--ip", "10.0.0.2",
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 || stdout.String() != "login ok\n" || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
}

func TestInvalidConfigurationReturnsUsageError(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CAMPUSLINK_TIMEOUT", "not-a-number")

	var stdout, stderr bytes.Buffer
	if exitCode := run([]string{"-u", "user", "-p", "pass"}, strings.NewReader(""), &stdout, &stderr); exitCode != 2 {
		t.Fatalf("exit code = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "invalid CAMPUSLINK_TIMEOUT") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestTimeoutFlagOverridesInvalidEnvironment(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CAMPUSLINK_TIMEOUT", "not-a-number")
	server := mockPortal(t, `{"code":0,"msg":"认证成功"}`)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{
		"--base-url", server.URL,
		"-u", "user",
		"-p", "pass",
		"--ip", "10.0.0.2",
		"--timeout", "5",
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
}

func TestPasswordStdinRejectsOtherPasswordSources(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CAMPUSLINK_PASSWORD", "from-env")

	var stdout, stderr bytes.Buffer
	if exitCode := run([]string{"--password-stdin"}, strings.NewReader("from-stdin\n"), &stdout, &stderr); exitCode != 2 {
		t.Fatalf("exit code = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestVersionDoesNotRequireValidEnvironment(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CAMPUSLINK_TIMEOUT", "invalid")
	oldVersion := version
	oldCommit := commit
	version = "v1.2.3"
	commit = "1234567890abcdef"
	t.Cleanup(func() {
		version = oldVersion
		commit = oldCommit
	})

	var stdout, stderr bytes.Buffer
	if exitCode := run([]string{"--version"}, strings.NewReader(""), &stdout, &stderr); exitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", exitCode, stderr.String())
	}
	if stdout.String() != "campuslink v1.2.3 (commit 1234567890ab)\n" {
		t.Fatalf("unexpected version output: %q", stdout.String())
	}
}

func TestReadPassword(t *testing.T) {
	password, err := readPassword(strings.NewReader("contains spaces\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if password != "contains spaces" {
		t.Fatalf("password = %q", password)
	}
	if _, err := readPassword(strings.NewReader("\n")); err == nil {
		t.Fatal("expected empty password to fail")
	}
}

func TestPortalErrorsAreActionable(t *testing.T) {
	for _, tc := range []struct{ response, message string }{
		{`{"code":1,"msg":"账号或密码错误"}`, "账号或密码错误"},
		{`{"code":2,"msg":"验证码"}`, "captcha required"},
		{`{"code":0,"isChangePwd":1}`, "password change required"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			clearConfigEnv(t)
			server := mockPortal(t, tc.response)
			defer server.Close()
			var stdout, stderr bytes.Buffer
			exitCode := run([]string{"--base-url", server.URL, "-u", "user", "-p", "pass"}, strings.NewReader(""), &stdout, &stderr)
			if exitCode != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.message) {
				t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRemovedFlagsAreRejected(t *testing.T) {
	clearConfigEnv(t)
	for _, flag := range []string{"--protocol", "--ac-id", "--gateway-ip", "--host"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{flag, "unused"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Fatalf("%s: exit=%d, want 2", flag, code)
		}
	}
}

func TestCredentialSources(t *testing.T) {
	for _, tc := range []struct {
		name, envUser, envPassword, stdin, wantUser, wantPassword string
		args                                                      []string
	}{
		{name: "environment", envUser: "env-user", envPassword: "env-pass", wantUser: "env-user", wantPassword: "env-pass"},
		{name: "flags override environment", envUser: "env-user", envPassword: "env-pass", args: []string{"-u", "first", "--username", "flag-user", "--password", "flag-pass"}, wantUser: "flag-user", wantPassword: "flag-pass"},
		{name: "stdin", envUser: "env-user", args: []string{"--password-stdin"}, stdin: " stdin-pass \n", wantUser: "env-user", wantPassword: " stdin-pass "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("CAMPUSLINK_USERNAME", tc.envUser)
			t.Setenv("CAMPUSLINK_PASSWORD", tc.envPassword)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/csrf-token":
					fmt.Fprint(w, `{"csrf_token":"test"}`)
				case "/api/account/status":
					fmt.Fprint(w, `{"code":1}`)
				case "/api/account/check", "/api/account/login":
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.PostForm.Get("username") != tc.wantUser || r.PostForm.Get("password") != tc.wantPassword {
						t.Error("incorrect credential source")
					}
					fmt.Fprint(w, `{"code":0}`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			}))
			defer server.Close()
			t.Setenv("CAMPUSLINK_BASE_URL", server.URL)
			t.Setenv("CAMPUSLINK_NAS_ID", "7")
			t.Setenv("CAMPUSLINK_IP", "10.0.0.2")
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, strings.NewReader(tc.stdin), &stdout, &stderr); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, &stderr)
			}
		})
	}
}

func TestOldCredentialEnvironmentIsNotRead(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("SRUN_USERNAME", "old-user")
	t.Setenv("SRUN_PASSWORD", "old-pass")
	var stdout, stderr bytes.Buffer
	if code := run(nil, strings.NewReader(""), &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "username is required") {
		t.Fatalf("exit=%d stderr=%s", code, &stderr)
	}
}

func mockPortal(t *testing.T, portalResult string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/r/default":
			http.Redirect(w, r, "/login?ip=10.0.0.2&nasId=7", http.StatusFound)
		case "/login":
			fmt.Fprint(w, "login")
		case "/api/csrf-token":
			fmt.Fprint(w, `{"csrf_token":"test-token"}`)
		case "/api/account/status":
			fmt.Fprint(w, `{"code":1,"msg":"离线"}`)
		case "/api/account/check":
			fmt.Fprint(w, `{"code":0,"isChangePwd":0}`)
		case "/api/account/login":
			fmt.Fprint(w, portalResult)
		default:
			http.NotFound(w, r)
		}
	}))
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CAMPUSLINK_USERNAME",
		"CAMPUSLINK_PASSWORD",
		"CAMPUSLINK_IP",
		"CAMPUSLINK_BASE_URL",
		"CAMPUSLINK_TIMEOUT",
		"CAMPUSLINK_NAS_ID",
		"CAMPUSLINK_ISP",
	} {
		t.Setenv(key, "")
	}
}

package autostart

import (
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cugcs632/CampusLink/internal/profile"
)

func TestDefinitionsAndLifecycle(t *testing.T) {
	for _, platform := range []string{"windows", "darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg config"))
			store, _ := profile.Open(filepath.Join(root, "data & user's $path%"))
			var calls []string
			m := &Manager{OS: platform, Home: root, UserID: "1000", Store: store, Run: func(name string, args ...string) (string, error) {
				calls = append(calls, name+" "+strings.Join(args, " "))
				return "Ready", nil
			}}
			definition, timer := m.Definition()
			if strings.Contains(definition, "password") || strings.Contains(definition, "CAMPUSLINK_PASSWORD") {
				t.Fatal("task contains credentials")
			}
			if !strings.Contains(definition, "background") || !strings.Contains(definition, "--config-dir") {
				t.Fatal("task does not use stable profile")
			}
			if platform != "linux" {
				decoder := xml.NewDecoder(strings.NewReader(definition))
				for {
					_, err := decoder.Token()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
				}
			} else if !strings.Contains(definition, "$$path%%") || !strings.Contains(timer, "OnUnitInactiveSec=5min") {
				t.Fatal("systemd escaping or interval is incorrect")
			}
			if platform == "windows" && (!strings.Contains(definition, "IgnoreNew") || !strings.Contains(definition, "InteractiveToken") || !strings.Contains(definition, "EventID=10000")) {
				t.Fatal("missing Windows scheduler safeguards")
			}
			source := filepath.Join(root, "source")
			if err := os.WriteFile(source, []byte("test-executable"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := m.Enable(source); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(m.Executable())
			if err != nil || string(data) != "test-executable" {
				t.Fatal("stable executable not installed")
			}
			if platform == "darwin" && runtime.GOOS == "darwin" {
				if out, err := exec.Command("plutil", "-lint", m.taskPath()).CombinedOutput(); err != nil {
					t.Fatalf("invalid plist: %s %v", out, err)
				}
			}
			if state, err := m.Status(); err != nil || state != "enabled" {
				t.Fatalf("status=%s err=%v", state, err)
			}
			if err := m.Enable(source); err != nil {
				t.Fatalf("repeat enable failed: %v", err)
			}
			if err := m.Disable(); err != nil {
				t.Fatal(err)
			}
			if err := m.Disable(); err != nil {
				t.Fatalf("repeat disable failed: %v", err)
			}
			if state, err := m.Status(); err != nil || state != "disabled" {
				t.Fatalf("status=%s err=%v", state, err)
			}
			if len(calls) == 0 {
				t.Fatal("scheduler never invoked")
			}
		})
	}
}

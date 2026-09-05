package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cugcs632/CampusLink/internal/autostart"
	"github.com/cugcs632/CampusLink/internal/portal"
	"github.com/cugcs632/CampusLink/internal/profile"
	"golang.org/x/term"
)

type application struct {
	store          *profile.Store
	stdin          io.Reader
	stdout, stderr io.Writer
	manager        func() (*autostart.Manager, error)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	dir := os.Getenv("CAMPUSLINK_CONFIG_DIR")
	if len(args) > 0 && args[0] == "--config-dir" {
		if len(args) < 2 || args[1] == "" {
			fmt.Fprintln(stderr, "error: --config-dir requires a directory before the command")
			return 2
		}
		dir, args = args[1], args[2:]
	}
	store, err := profile.Open(dir)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	a := &application{store: store, stdin: stdin, stdout: stdout, stderr: stderr}
	a.manager = func() (*autostart.Manager, error) { return autostart.New(store) }
	if len(args) == 0 {
		return a.login(nil)
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, "CampusLink — campus network authentication\n\nUsage: campuslink [--config-dir DIR] COMMAND [OPTIONS]\n\n  setup               Configure credentials and automatic login\n  login               Authenticate once (also the default command)\n  status              Show gateway and automatic login status\n  autostart enable    Install/update automatic login and resume retries\n  autostart disable   Remove automatic login; keep saved settings\n  logs                Show recent activity without credentials\n  doctor              Diagnose settings, credential access, DNS and HTTPS\n\nUse campuslink COMMAND --help for options.\nEnvironment variables: CAMPUSLINK_USERNAME, CAMPUSLINK_PASSWORD,\nCAMPUSLINK_BASE_URL, CAMPUSLINK_IP, CAMPUSLINK_NAS_ID, CAMPUSLINK_ISP,\nCAMPUSLINK_TIMEOUT, CAMPUSLINK_CONFIG_DIR.")
		return 0
	case "login":
		return a.login(args[1:])
	case "setup":
		return a.setup(args[1:])
	case "status":
		return a.inspect(args[1:], false)
	case "doctor":
		return a.inspect(args[1:], true)
	case "autostart":
		return a.autostart(args[1:])
	case "logs":
		if len(args) != 1 {
			return a.fail(errors.New("usage: campuslink logs"), 2)
		}
		data, err := store.Logs()
		if err != nil {
			return a.fail(err, 1)
		}
		fmt.Fprint(stdout, string(data))
		return 0
	case "background":
		if len(args) != 1 {
			return a.fail(errors.New("background takes no options"), 2)
		}
		return a.background()
	default:
		if strings.HasPrefix(args[0], "-") {
			return a.login(args)
		}
		return a.fail(fmt.Errorf("unknown command %q; run campuslink help", args[0]), 2)
	}
}

func (a *application) fail(err error, code int) int {
	fmt.Fprintln(a.stderr, "error:", err)
	return code
}

func connectionConfig(saved profile.Config) portal.Config {
	c := portal.DefaultConfig()
	if saved.BaseURL != "" {
		c.BaseURL = saved.BaseURL
	}
	c.NASID, c.ISP = saved.NASID, saved.ISP
	if saved.Timeout != 0 {
		c.Timeout = time.Duration(saved.Timeout) * time.Second
	}
	return c
}

func samePortal(first, second string) bool {
	canonical := func(value string) string {
		if !strings.Contains(value, "://") {
			value = "https://" + value
		}
		u, err := url.Parse(value)
		if err != nil {
			return ""
		}
		if strings.EqualFold(u.Hostname(), portal.DefaultHost) && u.Port() == "" {
			u.Scheme = "https"
		}
		return strings.ToLower(u.Scheme+"://"+u.Host) + strings.TrimRight(u.Path, "/")
	}
	return canonical(first) != "" && canonical(first) == canonical(second)
}

func (a *application) setup(args []string) int {
	saved, err := a.store.Load()
	if err != nil {
		fmt.Fprintln(a.stderr, "warning: saved configuration is invalid; setup will replace it after verification")
		saved = profile.Config{}
	}
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	username := flags.String("username", saved.Username, "campus network username; prompted when omitted")
	base := flags.String("base-url", connectionConfig(saved).BaseURL, "portal base URL")
	storage := flags.String("storage", "keyring", "password storage: keyring or file (plaintext with restricted permissions)")
	passwordStdin := flags.Bool("password-stdin", false, "read one password line from stdin")
	noAuto := flags.Bool("no-autostart", false, "save configuration without enabling automatic login")
	flags.StringVar(&saved.NASID, "nas-id", saved.NASID, "NAS ID; auto-discovered when empty")
	flags.StringVar(&saved.IP, "ip", saved.IP, "client IP; auto-discovered when empty")
	flags.StringVar(&saved.ISP, "isp", saved.ISP, "optional operator ID")
	if saved.Timeout == 0 {
		saved.Timeout = 8
	}
	flags.IntVar(&saved.Timeout, "timeout", saved.Timeout, "HTTP/DNS timeout in seconds (1-3600)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		return a.fail(errors.New("unexpected setup arguments"), 2)
	}
	if *storage != "keyring" && *storage != "file" {
		return a.fail(errors.New("storage must be keyring or file"), 2)
	}
	if saved.Timeout < 1 || saved.Timeout > maxTimeoutSeconds {
		return a.fail(errors.New("timeout must be between 1 and 3600 seconds"), 2)
	}
	reader := bufio.NewReader(a.stdin)
	if *username == "" {
		fmt.Fprint(a.stdout, "Username: ")
		value, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return a.fail(err, 1)
		}
		*username = strings.TrimSpace(value)
	}
	var password string
	if *passwordStdin {
		password, err = readPassword(reader)
	} else {
		file, ok := a.stdin.(*os.File)
		if !ok || !term.IsTerminal(int(file.Fd())) {
			return a.fail(errors.New("setup needs a terminal for hidden password input; use --password-stdin for a pipe"), 2)
		}
		fmt.Fprint(a.stdout, "Password (hidden): ")
		var data []byte
		data, err = term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(a.stdout)
		password = string(data)
	}
	if err != nil {
		return a.fail(err, 1)
	}
	if *username == "" || password == "" {
		return a.fail(errors.New("username and password are required"), 2)
	}
	c := saved
	c.Username, c.BaseURL, c.Storage = *username, *base, *storage
	if c.Timeout == 0 {
		c.Timeout = 8
	}
	config := connectionConfig(c)
	config.Warn = func(message string) { fmt.Fprintln(a.stderr, "warning:", message) }
	client, err := portal.NewClient(config)
	if err != nil {
		return a.fail(err, 2)
	}
	unlock, err := a.store.Lock()
	if err != nil {
		return a.fail(fmt.Errorf("cannot lock configuration: %w", err), 1)
	}
	result, loginErr := client.Login(c.Username, password, c.IP)
	if loginErr != nil || !portal.OK(result) {
		unlock()
		if loginErr != nil {
			return a.fail(loginErr, 1)
		}
		return a.fail(errors.New("authentication rejected; check the account in your browser before running setup again"), 1)
	}
	if err = a.store.Save(c, password); err == nil {
		err = a.record(result, nil, false)
	}
	unlock()
	if err != nil {
		return a.fail(err, 1)
	}
	fmt.Fprintln(a.stdout, "Configuration saved. Gateway accepted authentication or the device was already online.")
	fmt.Fprintln(a.stdout, "If already online, the password has not been verified by a fresh login.")
	if *storage == "file" {
		fmt.Fprintln(a.stdout, "Password storage: plaintext file restricted to your user (and system administrators).")
	}
	if *noAuto {
		return 0
	}
	fmt.Fprint(a.stdout, "Enable automatic login after signing in? [Y/n]: ")
	answer, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintln(a.stdout, "Run campuslink autostart enable when ready.")
		return 0
	}
	if value := strings.ToLower(strings.TrimSpace(answer)); value != "" && value != "y" && value != "yes" {
		return 0
	}
	return a.autostart([]string{"enable"})
}

func (a *application) autostart(args []string) int {
	if len(args) != 1 || (args[0] != "enable" && args[0] != "disable") {
		if len(args) == 1 && args[0] == "--help" {
			fmt.Fprintln(a.stdout, "Usage: campuslink autostart enable|disable")
			return 0
		}
		return a.fail(errors.New("usage: campuslink autostart enable|disable"), 2)
	}
	manager, err := a.manager()
	if err != nil {
		return a.fail(err, 1)
	}
	if args[0] == "disable" {
		if err := manager.Disable(); err != nil {
			return a.fail(err, 1)
		}
		fmt.Fprintln(a.stdout, "Automatic login disabled. Saved settings and credentials retained.")
		return 0
	}
	c, err := a.store.Load()
	if err != nil {
		return a.fail(err, 1)
	}
	if c.Version != 1 {
		return a.fail(errors.New("run campuslink setup first"), 1)
	}
	if _, err := a.store.Password(c); err != nil {
		return a.fail(err, 1)
	}
	executable, err := os.Executable()
	if err != nil {
		return a.fail(err, 1)
	}
	unlock, err := a.store.Lock()
	if err != nil {
		return a.fail(fmt.Errorf("cannot lock configuration: %w", err), 1)
	}
	state, err := a.store.ReadState()
	if err == nil {
		state.Paused = ""
		err = a.store.WriteJSON("state.json", state)
	}
	unlock()
	if err != nil {
		return a.fail(err, 1)
	}
	if err := manager.Enable(executable); err != nil {
		return a.fail(err, 1)
	}
	fmt.Fprintln(a.stdout, "Automatic login enabled for your user session. Network checks repeat every 5 minutes.")
	fmt.Fprintln(a.stdout, "Installed executable:", manager.Executable())
	return 0
}

func (a *application) background() int {
	unlock, err := a.store.Lock()
	if err != nil {
		return 0
	} // Another instance may be authenticating.
	defer unlock()
	state, err := a.store.ReadState()
	if err != nil {
		_ = a.store.Log("cannot read state; run campuslink doctor")
		return 1
	}
	if state.Paused != "" {
		return 0
	}
	c, err := a.store.Load()
	if err != nil || c.Version != 1 {
		_ = a.store.Log("configuration unavailable; run campuslink setup")
		return 1
	}
	password, err := a.store.Password(c)
	if err != nil {
		_ = a.store.Log("credential store unavailable; run campuslink doctor")
		return 1
	}
	config := connectionConfig(c)
	config.Warn = func(message string) { _ = a.store.Log("warning: " + message) }
	client, err := portal.NewClient(config)
	if err != nil {
		_ = a.store.Log("invalid connection configuration; run campuslink doctor")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, loginErr := client.LoginContext(ctx, c.Username, password, c.IP)
	if err := a.record(result, loginErr, true); err != nil {
		return 1
	}
	if loginErr != nil || !portal.OK(result) {
		return 1
	}
	return 0
}

func (a *application) record(result map[string]any, loginErr error, background bool) error {
	state, err := a.store.ReadState()
	if err != nil {
		return err
	}
	state.LastAttempt = time.Now()
	message := "connection failed"
	if background {
		message += "; will retry on the next scheduled run"
	}
	if loginErr == nil && portal.OK(result) {
		state.LastSuccess, state.Paused = state.LastAttempt, ""
		message = "online"
	} else if loginErr == nil {
		message = "authentication rejected; check credentials or complete browser verification"
		if background {
			state.Paused = message
		}
	}
	if err := a.store.WriteJSON("state.json", state); err != nil {
		return err
	}
	return a.store.Log(message)
}

func (a *application) inspect(args []string, doctor bool) int {
	saved, err := a.store.Load()
	if err != nil {
		return a.fail(err, 1)
	}
	config := connectionConfig(saved)
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	jsonOutput := flags.Bool("json", false, "print diagnostic status as JSON")
	flags.StringVar(&config.BaseURL, "base-url", getenv("CAMPUSLINK_BASE_URL", config.BaseURL), "portal base URL")
	flags.StringVar(&config.NASID, "nas-id", getenv("CAMPUSLINK_NAS_ID", config.NASID), "NAS ID; auto-discovered when empty")
	ip := flags.String("ip", getenv("CAMPUSLINK_IP", saved.IP), "client IP; auto-discovered when empty")
	seconds, envErr := timeoutFromEnv(int(config.Timeout / time.Second))
	flags.IntVar(&seconds, "timeout", seconds, "HTTP/DNS timeout in seconds (1-3600)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		return a.fail(errors.New("unexpected diagnostic arguments"), 2)
	}
	if envErr != nil && !flagWasSet(flags, "timeout") {
		return a.fail(envErr, 2)
	}
	if seconds < 1 || seconds > maxTimeoutSeconds {
		return a.fail(errors.New("timeout must be between 1 and 3600 seconds"), 2)
	}
	config.Timeout = time.Duration(seconds) * time.Second
	report := map[string]any{"configured": saved.Version == 1, "config_directory": a.store.Dir, "gateway": config.BaseURL}
	state, stateErr := a.store.ReadState()
	report["history"] = state
	if stateErr != nil {
		report["state_error"] = stateErr.Error()
	}
	manager, managerErr := a.manager()
	if managerErr == nil {
		report["autostart"], managerErr = manager.Status()
	}
	if managerErr != nil {
		report["autostart_error"] = managerErr.Error()
	}
	credentialOK := true
	if doctor {
		credentialOK = false
		if saved.Version == 1 {
			value, err := a.store.Password(saved)
			credentialOK = err == nil && value != ""
		}
		report["saved_credentials_accessible"] = credentialOK
	}
	var warnings []string
	config.Warn = func(message string) { warnings = append(warnings, message) }
	client, err := portal.NewClient(config)
	var status map[string]any
	if err == nil {
		status, err = client.StatusContext(context.Background(), *ip)
	}
	if err == nil {
		if code, ok := status["code"].(float64); !ok || (code != 0 && code != 1) {
			err = errors.New("unexpected gateway status code")
		}
	}
	report["gateway_reachable"] = err == nil
	report["online"] = err == nil && portal.OK(status)
	if err != nil {
		report["gateway_error"] = err.Error()
	}
	if len(warnings) > 0 {
		report["warnings"] = warnings
	}
	if *jsonOutput {
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Fprintln(a.stdout, string(data))
	} else {
		fmt.Fprintf(a.stdout, "Configuration: %s (saved: %v)\nGateway: %s\nOnline: %v\nAutomatic login: %v\n", a.store.Dir, saved.Version == 1, config.BaseURL, report["online"], report["autostart"])
		if !state.LastSuccess.IsZero() {
			fmt.Fprintln(a.stdout, "Last success:", state.LastSuccess.Format(time.RFC3339))
		}
		if state.Paused != "" {
			fmt.Fprintln(a.stdout, "Automatic login paused:", state.Paused, "— run login or autostart enable after resolving it")
		}
		if doctor {
			fmt.Fprintln(a.stdout, "Saved credentials accessible:", credentialOK)
		}
		for _, warning := range warnings {
			fmt.Fprintln(a.stderr, "warning:", warning)
		}
		for _, key := range []string{"gateway_error", "autostart_error", "state_error"} {
			if value, ok := report[key]; ok {
				fmt.Fprintln(a.stderr, key+":", value)
			}
		}
	}
	if err != nil || stateErr != nil || managerErr != nil || (doctor && !credentialOK) {
		return 1
	}
	return 0
}

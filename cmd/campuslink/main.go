package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/cugcs632/CampusLink/internal/portal"
)

const (
	maxPasswordBytes  = 1 << 20
	maxTimeoutSeconds = 3600
)

var (
	version = "dev"
	commit  = ""
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func (a *application) login(args []string) int {
	stdin, stdout, stderr := a.stdin, a.stdout, a.stderr
	saved, configErr := a.store.Load()
	defaultConfig := connectionConfig(saved)

	timeoutDefault, timeoutEnvErr := timeoutFromEnv(int(defaultConfig.Timeout / time.Second))

	var (
		username      string
		password      string
		ip            string
		baseURL       string
		timeoutSecs   int
		printJSON     bool
		passwordStdin bool
		printVersion  bool
		nasID         string
		isp           string
	)

	flags := flag.NewFlagSet("campuslink", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&username, "u", getenv("CAMPUSLINK_USERNAME", saved.Username), "campus network username")
	flags.StringVar(&username, "username", getenv("CAMPUSLINK_USERNAME", saved.Username), "campus network username")
	flags.StringVar(&password, "p", os.Getenv("CAMPUSLINK_PASSWORD"), "campus network password")
	flags.StringVar(&password, "password", os.Getenv("CAMPUSLINK_PASSWORD"), "campus network password")
	flags.BoolVar(&passwordStdin, "password-stdin", false, "read the campus network password from standard input")
	flags.StringVar(&ip, "ip", getenv("CAMPUSLINK_IP", saved.IP), "client IP; auto-discovered when empty")
	flags.StringVar(&baseURL, "base-url", getenv("CAMPUSLINK_BASE_URL", defaultConfig.BaseURL), "portal base URL; defaults to https://nap.cug.edu.cn")
	flags.StringVar(&nasID, "nas-id", getenv("CAMPUSLINK_NAS_ID", saved.NASID), "new portal NAS ID; auto-discovered when empty")
	flags.StringVar(&isp, "isp", getenv("CAMPUSLINK_ISP", saved.ISP), "optional operator ID; empty uses the campus network")
	flags.IntVar(&timeoutSecs, "timeout", timeoutDefault, "HTTP timeout in seconds (1-3600)")
	flags.BoolVar(&printJSON, "json", false, "print raw portal response as JSON")
	flags.BoolVar(&printVersion, "version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}
	if printVersion {
		fmt.Fprintf(stdout, "campuslink %s\n", currentVersion())
		return 0
	}
	if configErr != nil {
		fmt.Fprintln(stderr, "error:", configErr)
		return 2
	}
	if timeoutEnvErr != nil && !flagWasSet(flags, "timeout") {
		fmt.Fprintln(stderr, "error:", timeoutEnvErr)
		return 2
	}
	if timeoutSecs < 1 || timeoutSecs > maxTimeoutSeconds {
		fmt.Fprintf(stderr, "error: timeout must be between 1 and %d seconds\n", maxTimeoutSeconds)
		return 2
	}
	if passwordStdin {
		if password != "" {
			fmt.Fprintln(stderr, "error: --password-stdin cannot be combined with -p, --password, or CAMPUSLINK_PASSWORD")
			return 2
		}
		var err error
		password, err = readPassword(stdin)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
	}

	if !passwordStdin && !flagWasSet(flags, "p") && !flagWasSet(flags, "password") && os.Getenv("CAMPUSLINK_PASSWORD") == "" && saved.Version == 1 && username == saved.Username && samePortal(baseURL, saved.BaseURL) {
		var err error
		password, err = a.store.Password(saved)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	}
	if username == "" || password == "" {
		if username == "" {
			fmt.Fprintln(stderr, "error: username is required; run campuslink setup")
		} else {
			fmt.Fprintln(stderr, "error: password is required; run campuslink setup")
		}
		return 1
	}

	config := defaultConfig
	config.BaseURL = baseURL
	config.NASID = nasID
	config.ISP = isp
	config.Timeout = time.Duration(timeoutSecs) * time.Second
	config.Warn = func(message string) { fmt.Fprintln(stderr, "warning:", message) }
	client, err := portal.NewClient(config)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}

	unlock, err := a.store.Lock()
	if err != nil {
		fmt.Fprintln(stderr, "error: another operation is running or the configuration directory is not writable:", err)
		return 1
	}
	defer unlock()
	result, err := client.Login(username, password, ip)
	if recordErr := a.record(result, err, false); recordErr != nil {
		fmt.Fprintln(stderr, "warning: cannot record login status:", recordErr)
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	ok := portal.OK(result)
	if printJSON {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		fmt.Fprintln(stdout, string(encoded))
		if ok {
			return 0
		}
		return 1
	}

	if ok {
		fmt.Fprintln(stdout, "login ok")
		return 0
	}

	message := firstString(result, "msg", "error_msg", "message")
	if portal.PasswordChangeRequired(result) {
		message = "password change required; open the portal in a browser to update your password"
	} else if result["code"] == float64(2) {
		message = "captcha required; open the portal in a browser to complete verification"
	}
	if message == "" {
		encoded, _ := json.Marshal(result)
		message = string(encoded)
	}
	fmt.Fprintln(stderr, "login failed:", message)
	return 1
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func timeoutFromEnv(fallback int) (int, error) {
	value := os.Getenv("CAMPUSLINK_TIMEOUT")
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback, fmt.Errorf("invalid CAMPUSLINK_TIMEOUT %q: must be an integer number of seconds", value)
	}
	return parsed, nil
}

func flagWasSet(flags *flag.FlagSet, name string) bool {
	set := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == name {
			set = true
		}
	})
	return set
}

func readPassword(input io.Reader) (string, error) {
	reader := bufio.NewReader(io.LimitReader(input, maxPasswordBytes+1))
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password from standard input: %w", err)
	}
	if len(value) > maxPasswordBytes {
		return "", errors.New("password from standard input is too large")
	}
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	if value == "" {
		return "", errors.New("password from standard input is empty")
	}
	return value, nil
}

func currentVersion() string {
	buildVersion := version
	if buildVersion == "" {
		buildVersion = "dev"
	}
	buildCommit := commit
	modified := false
	if info, ok := debug.ReadBuildInfo(); ok {
		if buildVersion == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			buildVersion = strings.TrimSuffix(info.Main.Version, "+dirty")
		}
		if buildCommit == "" {
			for _, setting := range info.Settings {
				switch setting.Key {
				case "vcs.revision":
					buildCommit = setting.Value
				case "vcs.modified":
					modified = setting.Value == "true"
				}
			}
		}
	}
	if buildCommit == "" {
		return buildVersion
	}
	if len(buildCommit) > 12 {
		buildCommit = buildCommit[:12]
	}
	if modified {
		buildCommit += "-dirty"
	}
	return fmt.Sprintf("%s (commit %s)", buildVersion, buildCommit)
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

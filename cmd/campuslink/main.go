package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/CUG-CS-632/CampusLink/internal/srun"
)

func main() {
	var (
		username    string
		password    string
		ip          string
		host        string
		acID        string
		timeoutSecs int
		printJSON   bool
	)

	defaultConfig := srun.DefaultConfig()
	flag.StringVar(&username, "u", os.Getenv("SRUN_USERNAME"), "campus network username")
	flag.StringVar(&username, "username", os.Getenv("SRUN_USERNAME"), "campus network username")
	flag.StringVar(&password, "p", os.Getenv("SRUN_PASSWORD"), "campus network password")
	flag.StringVar(&password, "password", os.Getenv("SRUN_PASSWORD"), "campus network password")
	flag.StringVar(&ip, "ip", os.Getenv("SRUN_IP"), "client IP; auto-discovered when empty")
	flag.StringVar(&host, "host", getenv("SRUN_HOST", defaultConfig.Host), "SRun portal host")
	flag.StringVar(&acID, "ac-id", getenv("SRUN_AC_ID", defaultConfig.ACID), "SRun AC ID")
	flag.IntVar(&timeoutSecs, "timeout", getenvInt("SRUN_TIMEOUT", int(defaultConfig.Timeout/time.Second)), "HTTP timeout in seconds")
	flag.BoolVar(&printJSON, "json", false, "print raw portal response as JSON")
	flag.Parse()

	config := defaultConfig
	config.Host = host
	config.ACID = acID
	config.Timeout = time.Duration(timeoutSecs) * time.Second

	client, err := srun.NewClient(config)
	if err != nil {
		fail(err)
	}

	result, err := client.Login(username, password, ip)
	if err != nil {
		fail(err)
	}

	if printJSON {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fail(err)
		}
		fmt.Println(string(encoded))
		return
	}

	if srun.OK(result) {
		fmt.Println("login ok")
		return
	}

	message := firstString(result, "error_msg", "message")
	if message == "" {
		encoded, _ := json.Marshal(result)
		message = string(encoded)
	}
	fmt.Fprintln(os.Stderr, "login failed:", message)
	os.Exit(1)
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getenvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

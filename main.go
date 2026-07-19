package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

var (
	ansiPattern   = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	cpuPattern    = regexp.MustCompile(`(?im)Current:\s*([0-9]+(?:\.[0-9]+)?)%`)
	loginPattern  = regexp.MustCompile(`(?im)(Press <Enter> to continue\.\.\.|Username:|Password:|Authentication Failed|[^\r\n]*[#>]\s*$)`)
	promptPattern = regexp.MustCompile(`(?m)[^\r\n]*[#>]\s*$`)
	metricEscaper = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"")
)

type config struct {
	host        string
	port        string
	username    string
	password    string
	device      string
	fingerprint string
	command     string
	listen      string
	interval    time.Duration
	timeout     time.Duration
	debug       bool
}

type state struct {
	mu          sync.RWMutex
	cpu         float64
	hasCPU      bool
	success     bool
	duration    time.Duration
	lastSuccess time.Time
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	var current state
	scrape := func() {
		started := time.Now()
		cpu, err := scrapeCPU(cfg)
		current.mu.Lock()
		current.duration = time.Since(started)
		current.success = err == nil
		if err == nil {
			current.cpu = cpu
			current.hasCPU = true
			current.lastSuccess = time.Now()
		}
		current.mu.Unlock()
		if err != nil {
			log.Printf("switch scrape failed: %v", err)
		}
	}

	scrape()
	go func() {
		ticker := time.NewTicker(cfg.interval)
		defer ticker.Stop()
		for range ticker.C {
			scrape()
		}
	}()

	http.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		current.mu.RLock()
		defer current.mu.RUnlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		label := metricEscaper.Replace(cfg.device)
		fmt.Fprintln(w, "# HELP switch_cli_scrape_success Whether the latest CLI scrape succeeded (1) or failed (0).")
		fmt.Fprintln(w, "# TYPE switch_cli_scrape_success gauge")
		fmt.Fprintf(w, "switch_cli_scrape_success{device=\"%s\"} %d\n", label, boolNumber(current.success))
		fmt.Fprintln(w, "# HELP switch_cli_scrape_duration_seconds Duration of the latest CLI scrape.")
		fmt.Fprintln(w, "# TYPE switch_cli_scrape_duration_seconds gauge")
		fmt.Fprintf(w, "switch_cli_scrape_duration_seconds{device=\"%s\"} %.6f\n", label, current.duration.Seconds())
		fmt.Fprintln(w, "# HELP switch_cli_last_success_timestamp_seconds Unix timestamp of the latest successful CLI scrape.")
		fmt.Fprintln(w, "# TYPE switch_cli_last_success_timestamp_seconds gauge")
		fmt.Fprintf(w, "switch_cli_last_success_timestamp_seconds{device=\"%s\"} %.0f\n", label, float64(current.lastSuccess.Unix()))
		if current.hasCPU {
			fmt.Fprintln(w, "# HELP switch_cpu_utilization_percent Current switch CPU utilization reported by the CLI.")
			fmt.Fprintln(w, "# TYPE switch_cpu_utilization_percent gauge")
			fmt.Fprintf(w, "switch_cpu_utilization_percent{device=\"%s\"} %g\n", label, current.cpu)
		}
	})
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})

	server := &http.Server{Addr: cfg.listen, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("listening on %s for device %s", cfg.listen, cfg.device)
	log.Fatal(server.ListenAndServe())
}

func loadConfig() (config, error) {
	cfg := config{
		host:        os.Getenv("SWITCH_HOST"),
		port:        envOr("SWITCH_PORT", "22"),
		username:    os.Getenv("SWITCH_USERNAME"),
		password:    os.Getenv("SWITCH_PASSWORD"),
		device:      envOr("SWITCH_DEVICE", "switch"),
		fingerprint: os.Getenv("SWITCH_HOST_KEY_SHA256"),
		command:     envOr("SWITCH_CPU_COMMAND", "show cpu utilization"),
		listen:      envOr("LISTEN_ADDR", ":9808"),
		debug:       os.Getenv("SWITCH_DEBUG") == "1",
	}
	var err error
	if cfg.interval, err = time.ParseDuration(envOr("SCRAPE_INTERVAL", "30s")); err != nil {
		return cfg, fmt.Errorf("invalid SCRAPE_INTERVAL: %w", err)
	}
	if cfg.timeout, err = time.ParseDuration(envOr("SCRAPE_TIMEOUT", "15s")); err != nil {
		return cfg, fmt.Errorf("invalid SCRAPE_TIMEOUT: %w", err)
	}
	if cfg.host == "" || cfg.username == "" || cfg.password == "" || cfg.fingerprint == "" {
		return cfg, errors.New("SWITCH_HOST, SWITCH_USERNAME, SWITCH_PASSWORD and SWITCH_HOST_KEY_SHA256 are required")
	}
	return cfg, nil
}

func scrapeCPU(cfg config) (float64, error) {
	raw, err := net.DialTimeout("tcp", net.JoinHostPort(cfg.host, cfg.port), cfg.timeout)
	if err != nil {
		return 0, fmt.Errorf("connect: %w", err)
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(cfg.timeout))

	sshCfg := &ssh.ClientConfig{
		User:    cfg.username,
		Timeout: cfg.timeout,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			actual := ssh.FingerprintSHA256(key)
			if actual != cfg.fingerprint {
				return fmt.Errorf("host key mismatch: got %s", actual)
			}
			return nil
		},
	}
	conn, chans, reqs, err := ssh.NewClientConn(raw, net.JoinHostPort(cfg.host, cfg.port), sshCfg)
	if err != nil {
		return 0, fmt.Errorf("SSH handshake: %w", err)
	}
	client := ssh.NewClient(conn, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return 0, fmt.Errorf("new session: %w", err)
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		return 0, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := session.RequestPty("vt100", 80, 24, ssh.TerminalModes{}); err != nil {
		return 0, fmt.Errorf("request PTY: %w", err)
	}
	if err := session.Shell(); err != nil {
		return 0, fmt.Errorf("start shell: %w", err)
	}
	reader := bufio.NewReader(stdout)

	if err := loginConsole(reader, stdin, cfg); err != nil {
		return 0, err
	}
	if _, err := fmt.Fprintf(stdin, "%s\r", cfg.command); err != nil {
		return 0, err
	}
	output, err := readUntil(reader, promptPattern, 64*1024)
	if err != nil {
		return 0, fmt.Errorf("read command output: %w", err)
	}
	return parseCPU(output)
}

func loginConsole(reader *bufio.Reader, writer io.Writer, cfg config) error {
	for step := 0; step < 12; step++ {
		output, err := readUntil(reader, loginPattern, 32*1024)
		if err != nil {
			return fmt.Errorf("console login: %w", err)
		}
		clean := cleanTerminal(output)
		if cfg.debug {
			log.Printf("console state: %q", strings.ReplaceAll(clean, cfg.password, "[REDACTED]"))
		}
		switch {
		case strings.Contains(clean, "Press <Enter> to continue..."):
			_, err = io.WriteString(writer, "\r")
		case strings.Contains(clean, "Username:"):
			_, err = fmt.Fprintf(writer, "%s\r", cfg.username)
		case strings.Contains(clean, "Password:"):
			_, err = fmt.Fprintf(writer, "%s\r", cfg.password)
		case strings.Contains(clean, "Authentication Failed"):
			continue
		case promptPattern.MatchString(clean):
			return nil
		default:
			continue
		}
		if err != nil {
			return err
		}
	}
	return errors.New("console login did not reach a command prompt")
}

func readUntil(reader *bufio.Reader, pattern *regexp.Regexp, limit int) (string, error) {
	var output strings.Builder
	buffer := make([]byte, 1024)
	for output.Len() < limit {
		n, err := reader.Read(buffer)
		if n > 0 {
			output.Write(buffer[:n])
			clean := cleanTerminal(output.String())
			if pattern.MatchString(clean) {
				return clean, nil
			}
		}
		if err != nil {
			return output.String(), err
		}
	}
	return output.String(), errors.New("console output exceeded safety limit")
}

func cleanTerminal(value string) string {
	return ansiPattern.ReplaceAllString(strings.ReplaceAll(value, "\r", ""), "")
}

func parseCPU(output string) (float64, error) {
	match := cpuPattern.FindStringSubmatch(cleanTerminal(output))
	if len(match) != 2 {
		return 0, errors.New("CPU value not found in command output")
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil || value < 0 || value > 100 {
		return 0, fmt.Errorf("invalid CPU value %q", match[1])
	}
	return value, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func boolNumber(value bool) int {
	if value {
		return 1
	}
	return 0
}

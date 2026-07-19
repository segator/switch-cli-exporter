package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var metricEscaper = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"")

type config struct {
	host     string
	username string
	password string
	device   string
	listen   string
	interval time.Duration
	timeout  time.Duration
}

type state struct {
	mu          sync.RWMutex
	cpu         float64
	mem         float64
	hasCPU      bool
	hasMemory   bool
	success     bool
	duration    time.Duration
	lastSuccess time.Time
}

type webClient struct {
	cfg    config
	client *http.Client
}

type loginInfoResponse struct {
	Data struct {
		Modulus string `json:"modulus"`
	} `json:"data"`
}

type loginStatusResponse struct {
	Data struct {
		Status string `json:"status"`
		Reason string `json:"failReason"`
	} `json:"data"`
}

type cpuMemoryResponse struct {
	Data struct {
		CPU float64 `json:"cpu"`
		Mem float64 `json:"mem"`
	} `json:"data"`
	Logout bool   `json:"logout"`
	Reason string `json:"reason"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	client := &webClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.timeout,
		},
	}
	var current state
	scrape := func() {
		started := time.Now()
		cpu, mem, err := client.scrape()
		current.mu.Lock()
		current.duration = time.Since(started)
		current.success = err == nil
		if err == nil {
			current.cpu = cpu
			current.mem = mem
			current.hasCPU = true
			current.hasMemory = true
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
		fmt.Fprintln(w, "# HELP switch_cli_scrape_success Whether the latest switch web scrape succeeded (1) or failed (0).")
		fmt.Fprintln(w, "# TYPE switch_cli_scrape_success gauge")
		fmt.Fprintf(w, "switch_cli_scrape_success{device=\"%s\"} %d\n", label, boolNumber(current.success))
		fmt.Fprintln(w, "# HELP switch_cli_scrape_duration_seconds Duration of the latest switch web scrape.")
		fmt.Fprintln(w, "# TYPE switch_cli_scrape_duration_seconds gauge")
		fmt.Fprintf(w, "switch_cli_scrape_duration_seconds{device=\"%s\"} %.6f\n", label, current.duration.Seconds())
		fmt.Fprintln(w, "# HELP switch_cli_last_success_timestamp_seconds Unix timestamp of the latest successful switch web scrape.")
		fmt.Fprintln(w, "# TYPE switch_cli_last_success_timestamp_seconds gauge")
		fmt.Fprintf(w, "switch_cli_last_success_timestamp_seconds{device=\"%s\"} %.0f\n", label, float64(current.lastSuccess.Unix()))
		if current.hasCPU {
			fmt.Fprintln(w, "# HELP switch_cpu_utilization_percent Current switch CPU utilization reported by the web UI API.")
			fmt.Fprintln(w, "# TYPE switch_cpu_utilization_percent gauge")
			fmt.Fprintf(w, "switch_cpu_utilization_percent{device=\"%s\"} %g\n", label, current.cpu)
		}
		if current.hasMemory {
			fmt.Fprintln(w, "# HELP switch_memory_used_percent Current switch memory utilization reported by the web UI API.")
			fmt.Fprintln(w, "# TYPE switch_memory_used_percent gauge")
			fmt.Fprintf(w, "switch_memory_used_percent{device=\"%s\"} %g\n", label, current.mem)
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
		host:     os.Getenv("SWITCH_HOST"),
		username: os.Getenv("SWITCH_USERNAME"),
		password: os.Getenv("SWITCH_PASSWORD"),
		device:   envOr("SWITCH_DEVICE", "switch"),
		listen:   envOr("LISTEN_ADDR", ":9808"),
	}
	var err error
	if cfg.interval, err = time.ParseDuration(envOr("SCRAPE_INTERVAL", "60s")); err != nil {
		return cfg, fmt.Errorf("invalid SCRAPE_INTERVAL: %w", err)
	}
	if cfg.timeout, err = time.ParseDuration(envOr("SCRAPE_TIMEOUT", "10s")); err != nil {
		return cfg, fmt.Errorf("invalid SCRAPE_TIMEOUT: %w", err)
	}
	if cfg.host == "" || cfg.username == "" || cfg.password == "" {
		return cfg, errors.New("SWITCH_HOST, SWITCH_USERNAME and SWITCH_PASSWORD are required")
	}
	return cfg, nil
}

func (c *webClient) scrape() (float64, float64, error) {
	result, err := c.getCPUMemory()
	if err != nil {
		return 0, 0, err
	}
	if result.Logout {
		if err := c.login(); err != nil {
			return 0, 0, err
		}
		result, err = c.getCPUMemory()
		if err != nil {
			return 0, 0, err
		}
	}
	if result.Data.CPU < 0 || result.Data.CPU > 100 || result.Data.Mem < 0 || result.Data.Mem > 100 {
		return 0, 0, fmt.Errorf("invalid switch values cpu=%v mem=%v", result.Data.CPU, result.Data.Mem)
	}
	return result.Data.CPU, result.Data.Mem, nil
}

func (c *webClient) login() error {
	var info loginInfoResponse
	if err := c.getJSON("cgi/get.cgi", map[string]string{"cmd": "home_login"}, &info); err != nil {
		return fmt.Errorf("web login info: %w", err)
	}
	modulus := new(big.Int)
	if _, ok := modulus.SetString(info.Data.Modulus, 16); !ok || modulus.Sign() <= 0 {
		return errors.New("web login returned an invalid RSA modulus")
	}
	encrypted, err := rsa.EncryptPKCS1v15(rand.Reader, &rsa.PublicKey{N: modulus, E: 65537}, []byte(c.cfg.password))
	if err != nil {
		return fmt.Errorf("encrypt web password: %w", err)
	}
	var auth struct {
		Status string `json:"status"`
	}
	if err := c.postJSON("cgi/set.cgi", map[string]string{
		"cmd": "home_loginAuth",
	}, map[string]string{
		"_ds":      "1",
		"username": c.cfg.username,
		"password": base64.StdEncoding.EncodeToString(encrypted),
		"_de":      "1",
	}, &auth); err != nil {
		return fmt.Errorf("web login auth: %w", err)
	}
	if auth.Status != "ok" {
		return fmt.Errorf("web login auth rejected: %s", auth.Status)
	}

	deadline := time.Now().Add(c.cfg.timeout)
	for time.Now().Before(deadline) {
		var status loginStatusResponse
		if err := c.getJSON("cgi/get.cgi", map[string]string{"cmd": "home_loginStatus"}, &status); err != nil {
			return fmt.Errorf("web login status: %w", err)
		}
		switch status.Data.Status {
		case "ok":
			return nil
		case "fail":
			return fmt.Errorf("web login failed: %s", status.Data.Reason)
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("web login timed out")
}

func (c *webClient) getCPUMemory() (cpuMemoryResponse, error) {
	var result cpuMemoryResponse
	err := c.getJSON("cgi/get.cgi", map[string]string{"cmd": "sys_cpumem"}, &result)
	return result, err
}

func (c *webClient) getJSON(path string, query map[string]string, result any) error {
	return c.requestJSON(http.MethodGet, path, query, nil, result)
}

func (c *webClient) postJSON(path string, query, form map[string]string, result any) error {
	return c.requestJSON(http.MethodPost, path, query, form, result)
}

func (c *webClient) requestJSON(method, path string, query, form map[string]string, result any) error {
	values := url.Values{}
	for key, value := range query {
		values.Set(key, value)
	}
	values.Set("dummy", fmt.Sprintf("%d", time.Now().UnixMilli()))
	endpoint := "http://" + c.cfg.host + "/" + path + "?" + values.Encode()
	var body io.Reader
	if form != nil {
		formValues := url.Values{}
		for key, value := range form {
			formValues.Set(key, value)
		}
		body = strings.NewReader(formValues.Encode())
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", "http://"+c.cfg.host+"/login.html")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("switch web returned HTTP %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode switch web response: %w", err)
	}
	return nil
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

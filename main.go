package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	version               = "0.3.0"
	defaultMaxOutputBytes = 1 * 1024 * 1024
	defaultMaxStreamBytes = 32 * 1024 * 1024
	defaultMaxFindResults = 1000
)

func main() {
	cfg, mCfg, err := loadFromArgs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[weir] %v\n", err)
		fmt.Fprintf(os.Stderr, "usage: weir --run-allow cmd1,cmd2,cmd3 [--timeout 60] [--max-lines 1000] [--max-bytes 1048576] [--max-stream-bytes 33554432] [--max-find-results 1000] [--find-deny /path1,/path2] [--metrics-dir path] [--metrics-retention 30d]\n")
		os.Exit(1)
	}

	metrics, err := NewMetricsCollector(mCfg.Dir, mCfg.Retention)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[weir] metrics: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if metrics != nil {
			metrics.Close()
		}
	}()

	fmt.Fprintf(os.Stderr, "[weir] ready (%d commands whitelisted)\n", len(cfg.Commands))
	server := NewServer(cfg, version, metrics)
	if err := server.Serve(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "[weir] error: %v\n", err)
		os.Exit(1)
	}
}

type metricsConfig struct {
	Dir       string
	Retention time.Duration
}

func defaultJobsDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "weir", "jobs")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "weir", "jobs")
}

func defaultMetricsDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "weir", "metrics")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "weir", "metrics")
}

func parseNonNegativeInt(value, flag string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative number", flag)
	}
	return n, nil
}

func loadFromArgs() (*Config, *metricsConfig, error) {
	args := os.Args[1:]
	var allowList string
	var findDenyPaths string
	timeout := 60
	maxLines := 1000
	maxBytes := defaultMaxOutputBytes
	maxStreamBytes := defaultMaxStreamBytes
	maxFindResults := defaultMaxFindResults
	metricsDir := defaultMetricsDir()
	metricsRetention := 30 * 24 * time.Hour

	for i := 0; i < len(args); i++ {
		flag := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", flag)
			}
			i++
			return args[i], nil
		}

		switch flag {
		case "--run-allow":
			value, err := next()
			if err != nil {
				return nil, nil, err
			}
			allowList = value
		case "--find-deny":
			value, err := next()
			if err != nil {
				return nil, nil, err
			}
			findDenyPaths = value
		case "--timeout", "--max-lines", "--max-bytes", "--max-stream-bytes", "--max-find-results":
			value, err := next()
			if err != nil {
				return nil, nil, err
			}
			n, err := parseNonNegativeInt(value, flag)
			if err != nil {
				return nil, nil, err
			}
			switch flag {
			case "--timeout":
				timeout = n
			case "--max-lines":
				maxLines = n
			case "--max-bytes":
				maxBytes = n
			case "--max-stream-bytes":
				maxStreamBytes = n
			case "--max-find-results":
				maxFindResults = n
			}
		case "--metrics-dir":
			value, err := next()
			if err != nil {
				return nil, nil, err
			}
			metricsDir = value
		case "--metrics-retention":
			value, err := next()
			if err != nil {
				return nil, nil, err
			}
			dur, err := parseRetention(value)
			if err != nil {
				return nil, nil, fmt.Errorf("--metrics-retention: %w", err)
			}
			metricsRetention = dur
		default:
			return nil, nil, fmt.Errorf("unknown argument: %s", flag)
		}
	}

	if allowList == "" {
		return nil, nil, fmt.Errorf("--run-allow is required")
	}

	cfg := &Config{
		Commands: make(map[string]CommandConfig),
		Defaults: DefaultConfig{
			MaxOutputLines: maxLines,
			MaxOutputBytes: maxBytes,
			MaxStreamBytes: maxStreamBytes,
			MaxFindResults: maxFindResults,
			Timeout:        timeout,
		},
		Jobs: JobConfig{Dir: defaultJobsDir(), MaxConcurrent: 2, LogBytes: maxBytes, Retention: 7 * 24 * time.Hour},
	}
	for _, entry := range strings.Split(allowList, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if colonIdx := strings.IndexByte(entry, ':'); colonIdx > 0 {
			name := entry[:colonIdx]
			subs := strings.Split(entry[colonIdx+1:], ";")
			var subcommands []string
			for _, subcommand := range subs {
				subcommand = strings.TrimSpace(subcommand)
				if subcommand != "" {
					subcommands = append(subcommands, subcommand)
				}
			}
			cfg.Commands[name] = CommandConfig{Subcommands: subcommands}
		} else {
			cfg.Commands[entry] = CommandConfig{}
		}
	}
	if findDenyPaths != "" {
		for _, path := range strings.Split(findDenyPaths, ",") {
			path = strings.TrimSpace(path)
			if path != "" {
				cfg.FindDenyPaths = append(cfg.FindDenyPaths, path)
			}
		}
	}

	return cfg, &metricsConfig{Dir: metricsDir, Retention: metricsRetention}, nil
}

// parseRetention parses a duration string like "30d", "7d", "24h".
func parseRetention(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("too short, use e.g. '30d' or '24h'")
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	n := 0
	for _, c := range numStr {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid number in %q", s)
		}
		n = n*10 + int(c-'0')
	}
	switch unit {
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown unit %q (use h, d, or w)", string(unit))
	}
}

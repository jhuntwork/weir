package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const version = "0.3.0"

func main() {
	cfg, mCfg, err := loadFromArgs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[weir] %v\n", err)
		fmt.Fprintf(os.Stderr, "usage: weir --run-allow cmd1,cmd2,cmd3 [--timeout 60] [--max-lines 1000] [--find-deny /path1,/path2] [--metrics-dir path] [--metrics-retention 30d]\n")
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

func loadFromArgs() (*Config, *metricsConfig, error) {
	args := os.Args[1:]

	var allowList string
	var findDenyPaths string
	timeout := 60
	maxLines := 1000
	metricsDir := defaultMetricsDir()
	metricsRetention := 30 * 24 * time.Hour

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--run-allow":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("--run-allow requires a value")
			}
			i++
			allowList = args[i]
		case "--find-deny":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("--find-deny requires a value")
			}
			i++
			findDenyPaths = args[i]
		case "--timeout":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("--timeout requires a value")
			}
			i++
			n := 0
			for _, c := range args[i] {
				if c < '0' || c > '9' {
					return nil, nil, fmt.Errorf("--timeout must be a number")
				}
				n = n*10 + int(c-'0')
			}
			timeout = n
		case "--max-lines":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("--max-lines requires a value")
			}
			i++
			n := 0
			for _, c := range args[i] {
				if c < '0' || c > '9' {
					return nil, nil, fmt.Errorf("--max-lines must be a number")
				}
				n = n*10 + int(c-'0')
			}
			maxLines = n
		case "--metrics-dir":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("--metrics-dir requires a value")
			}
			i++
			metricsDir = args[i]
		case "--metrics-retention":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("--metrics-retention requires a value")
			}
			i++
			dur, err := parseRetention(args[i])
			if err != nil {
				return nil, nil, fmt.Errorf("--metrics-retention: %w", err)
			}
			metricsRetention = dur
		default:
			return nil, nil, fmt.Errorf("unknown argument: %s", args[i])
		}
	}

	if allowList == "" {
		return nil, nil, fmt.Errorf("--run-allow is required")
	}

	cfg := &Config{
		Commands: make(map[string]CommandConfig),
		Defaults: DefaultConfig{
			MaxOutputLines: maxLines,
			Timeout:        timeout,
		},
	}
	for _, entry := range strings.Split(allowList, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// Support "cmd:sub1;sub2;sub3" syntax for subcommand filtering.
		if colonIdx := strings.IndexByte(entry, ':'); colonIdx > 0 {
			name := entry[:colonIdx]
			subs := strings.Split(entry[colonIdx+1:], ";")
			var subcommands []string
			for _, s := range subs {
				s = strings.TrimSpace(s)
				if s != "" {
					subcommands = append(subcommands, s)
				}
			}
			cfg.Commands[name] = CommandConfig{Subcommands: subcommands}
		} else {
			cfg.Commands[entry] = CommandConfig{}
		}
	}
	if findDenyPaths != "" {
		for _, p := range strings.Split(findDenyPaths, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				cfg.FindDenyPaths = append(cfg.FindDenyPaths, p)
			}
		}
	}

	mCfg := &metricsConfig{
		Dir:       metricsDir,
		Retention: metricsRetention,
	}

	return cfg, mCfg, nil
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

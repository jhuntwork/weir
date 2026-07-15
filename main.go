package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const version = "0.2.0"

func main() {
	cfg, err := loadFromArgs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[weir] %v\n", err)
		fmt.Fprintf(os.Stderr, "usage: weir --run-allow cmd1,cmd2,cmd3 [--timeout 60] [--max-lines 1000] [--find-deny /path1,/path2]\n")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "[weir] ready (%d commands whitelisted)\n", len(cfg.Commands))

	server := NewServer(cfg, version)
	if err := server.Serve(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "[weir] error: %v\n", err)
		os.Exit(1)
	}
}

func loadFromArgs() (*Config, error) {
	args := os.Args[1:]

	var allowList string
	var findDenyPaths string
	timeout := 60
	maxLines := 1000

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--run-allow":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--run-allow requires a value")
			}
			i++
			allowList = args[i]
		case "--find-deny":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--find-deny requires a value")
			}
			i++
			findDenyPaths = args[i]
		case "--timeout":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--timeout requires a value")
			}
			i++
			n := 0
			for _, c := range args[i] {
				if c < '0' || c > '9' {
					return nil, fmt.Errorf("--timeout must be a number")
				}
				n = n*10 + int(c-'0')
			}
			timeout = n
		case "--max-lines":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--max-lines requires a value")
			}
			i++
			n := 0
			for _, c := range args[i] {
				if c < '0' || c > '9' {
					return nil, fmt.Errorf("--max-lines must be a number")
				}
				n = n*10 + int(c-'0')
			}
			maxLines = n
		default:
			return nil, fmt.Errorf("unknown argument: %s", args[i])
		}
	}

	if allowList == "" {
		return nil, fmt.Errorf("--run-allow is required")
	}

	cfg := &Config{
		Commands: make(map[string]CommandConfig),
		Defaults: DefaultConfig{
			MaxOutputLines: maxLines,
			Timeout:        timeout,
		},
	}
	for _, name := range strings.Split(allowList, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			cfg.Commands[name] = CommandConfig{}
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
	return cfg, nil
}

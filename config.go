package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config defines the whitelist and default behavior.
type Config struct {
	Commands      map[string]CommandConfig
	Defaults      DefaultConfig
	FindDenyPaths []string // additional paths to reject in find tool
	Jobs          JobConfig
}

// JobConfig controls persisted, server-lifetime local job execution.
type JobConfig struct {
	Dir           string
	MaxConcurrent int
	LogBytes      int
	Retention     time.Duration
}

// CommandConfig is per-command settings.
type CommandConfig struct {
	Path           string
	Env            map[string]string
	MaxOutputLines int
	DefaultTimeout int
	Subcommands    []string // if non-empty, only these first-arg subcommands are allowed
}

// DefaultConfig holds global defaults.
type DefaultConfig struct {
	MaxOutputLines int
	MaxOutputBytes int
	MaxStreamBytes int
	MaxFindResults int
	Timeout        int
}

// IsAllowed checks whether a command basename is in the whitelist.
func (c *Config) IsAllowed(basename string) bool {
	_, ok := c.Commands[basename]
	return ok
}

// IsSubcommandAllowed checks whether the first argument is an allowed
// subcommand. Restricted commands intentionally use the canonical form
// "command subcommand [args...]"; flags before the subcommand are rejected.
func (c *Config) IsSubcommandAllowed(basename string, args []string) bool {
	cmd, ok := c.Commands[basename]
	if !ok {
		return false
	}
	if len(cmd.Subcommands) == 0 {
		return true // no restriction
	}
	sub := findSubcommand(args)
	if sub == "" {
		return false // subcommands required and must be first
	}
	for _, allowed := range cmd.Subcommands {
		if allowed == sub {
			return true
		}
	}
	return false
}

// findSubcommand returns the first argument only when it is a subcommand.
// Flags before a restricted command's subcommand are intentionally unsupported.
func findSubcommand(args []string) string {
	if len(args) == 0 || (len(args[0]) > 0 && args[0][0] == '-') {
		return ""
	}
	return args[0]
}

// Resolve returns the full path for a command.
func (c *Config) Resolve(basename string) (string, error) {
	if !c.IsAllowed(basename) {
		return "", fmt.Errorf("command %q not in whitelist", basename)
	}
	cmd := c.Commands[basename]
	if cmd.Path != "" {
		if _, err := os.Stat(cmd.Path); err != nil {
			return "", fmt.Errorf("configured path %q: %w", cmd.Path, err)
		}
		return cmd.Path, nil
	}
	return lookPath(basename)
}

// TimeoutFor returns the effective timeout for a command in seconds.
func (c *Config) TimeoutFor(basename string) int {
	if cmd, ok := c.Commands[basename]; ok && cmd.DefaultTimeout > 0 {
		return cmd.DefaultTimeout
	}
	return c.Defaults.Timeout
}

// MaxOutputFor returns the effective max output lines for a command.
func (c *Config) MaxOutputFor(basename string) int {
	if cmd, ok := c.Commands[basename]; ok && cmd.MaxOutputLines > 0 {
		return cmd.MaxOutputLines
	}
	return c.Defaults.MaxOutputLines
}

// EnvFor returns any extra env vars configured for a command.
func (c *Config) EnvFor(basename string) map[string]string {
	if cmd, ok := c.Commands[basename]; ok {
		return cmd.Env
	}
	return nil
}

func lookPath(name string) (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		full := filepath.Join(dir, name)
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			return full, nil
		}
	}
	return "", fmt.Errorf("%q not found in PATH", name)
}

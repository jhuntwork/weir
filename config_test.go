package main

import (
	"testing"
)

func TestIsAllowed(t *testing.T) {
	cfg := &Config{
		Commands: map[string]CommandConfig{
			"git":   {},
			"swamp": {},
		},
	}

	if !cfg.IsAllowed("git") {
		t.Fatal("expected git to be allowed")
	}
	if !cfg.IsAllowed("swamp") {
		t.Fatal("expected swamp to be allowed")
	}
	if cfg.IsAllowed("rm") {
		t.Fatal("expected rm to be rejected")
	}
	if cfg.IsAllowed("bash") {
		t.Fatal("expected bash to be rejected")
	}
}

func TestTimeoutFor(t *testing.T) {
	cfg := &Config{
		Commands: map[string]CommandConfig{
			"git":  {},
			"zig":  {DefaultTimeout: 300},
			"fast": {DefaultTimeout: 5},
		},
		Defaults: DefaultConfig{Timeout: 60},
	}

	if got := cfg.TimeoutFor("git"); got != 60 {
		t.Fatalf("git timeout: got %d, want 60", got)
	}
	if got := cfg.TimeoutFor("zig"); got != 300 {
		t.Fatalf("zig timeout: got %d, want 300", got)
	}
	if got := cfg.TimeoutFor("fast"); got != 5 {
		t.Fatalf("fast timeout: got %d, want 5", got)
	}
}

func TestMaxOutputFor(t *testing.T) {
	cfg := &Config{
		Commands: map[string]CommandConfig{
			"git":   {MaxOutputLines: 500},
			"swamp": {},
		},
		Defaults: DefaultConfig{MaxOutputLines: 1000},
	}

	if got := cfg.MaxOutputFor("git"); got != 500 {
		t.Fatalf("git max output: got %d, want 500", got)
	}
	if got := cfg.MaxOutputFor("swamp"); got != 1000 {
		t.Fatalf("swamp max output: got %d, want 1000 (default)", got)
	}
}

func TestResolve(t *testing.T) {
	cfg := &Config{
		Commands: map[string]CommandConfig{
			"git":      {},
			"noexist":  {},
			"explicit": {Path: "/bin/sh"},
		},
	}

	// git should resolve via PATH
	path, err := cfg.Resolve("git")
	if err != nil {
		t.Fatalf("resolve git: %v", err)
	}
	if path == "" {
		t.Fatal("resolve git: empty path")
	}

	// explicit path
	path, err = cfg.Resolve("explicit")
	if err != nil {
		t.Fatalf("resolve explicit: %v", err)
	}
	if path != "/bin/sh" {
		t.Fatalf("resolve explicit: got %q, want /bin/sh", path)
	}

	// not in whitelist
	_, err = cfg.Resolve("rm")
	if err == nil {
		t.Fatal("resolve rm: expected error")
	}
}

func TestIsSubcommandAllowed(t *testing.T) {
	cfg := &Config{
		Commands: map[string]CommandConfig{
			"git":   {Subcommands: []string{"diff", "log", "status"}},
			"swamp": {}, // no subcommand restriction
		},
	}

	// Allowed subcommands pass.
	if !cfg.IsSubcommandAllowed("git", []string{"status"}) {
		t.Fatal("expected 'git status' to be allowed")
	}
	if !cfg.IsSubcommandAllowed("git", []string{"log", "--oneline"}) {
		t.Fatal("expected 'git log --oneline' to be allowed")
	}
	if !cfg.IsSubcommandAllowed("git", []string{"diff", "HEAD~1"}) {
		t.Fatal("expected 'git diff HEAD~1' to be allowed")
	}

	// Disallowed subcommand fails.
	if cfg.IsSubcommandAllowed("git", []string{"push"}) {
		t.Fatal("expected 'git push' to be denied")
	}

	// No args fails when subcommands are restricted.
	if cfg.IsSubcommandAllowed("git", []string{}) {
		t.Fatal("expected bare 'git' with no subcommand to be denied")
	}

	// No restriction means anything passes.
	if !cfg.IsSubcommandAllowed("swamp", []string{"model", "list"}) {
		t.Fatal("expected unrestricted command to allow any subcommand")
	}

	// Flags before the subcommand are intentionally rejected so the direct
	// command form remains canonical and unambiguous.
	if cfg.IsSubcommandAllowed("git", []string{"--git-dir=/tmp/repo/.git", "status"}) {
		t.Fatal("expected flags before restricted subcommand to be denied")
	}
	if cfg.IsSubcommandAllowed("git", []string{"--no-pager", "log"}) {
		t.Fatal("expected flags before restricted subcommand to be denied")
	}
	if cfg.IsSubcommandAllowed("git", []string{"--git-dir", "status", "config"}) {
		t.Fatal("expected option-value subcommand ambiguity to be denied")
	}

	// Leading flags with a disallowed subcommand still fails.
	if cfg.IsSubcommandAllowed("git", []string{"--no-pager", "push"}) {
		t.Fatal("expected 'git --no-pager push' to be denied")
	}

	// Only flags, no subcommand — fails.
	if cfg.IsSubcommandAllowed("git", []string{"--version"}) {
		t.Fatal("expected 'git --version' with no subcommand to be denied")
	}
}

func TestFindSubcommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"simple", []string{"status"}, "status"},
		{"flag then sub is rejected", []string{"--git-dir=/path", "log"}, ""},
		{"multiple flags are rejected", []string{"--no-pager", "-c", "diff"}, ""},
		{"no subcommand", []string{"--version"}, ""},
		{"empty", []string{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findSubcommand(tc.args)
			if got != tc.want {
				t.Fatalf("findSubcommand(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

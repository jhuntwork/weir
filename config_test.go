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

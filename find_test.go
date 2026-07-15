package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupFindFixture creates a temp directory tree for testing:
//
//	root/
//	  file1.go       (100 bytes)
//	  file2.txt      (5000 bytes)
//	  .hidden        (10 bytes)
//	  sub/
//	    file3.go     (200 bytes)
//	    deep/
//	      file4.go   (50 bytes)
//	  node_modules/
//	    pkg.json     (30 bytes)
//	  empty_dir/
func setupFindFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Create directories.
	dirs := []string{"sub", "sub/deep", "node_modules", "empty_dir"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Create files with specific sizes.
	files := map[string]int{
		"file1.go":              100,
		"file2.txt":             5000,
		".hidden":               10,
		"sub/file3.go":          200,
		"sub/deep/file4.go":     50,
		"node_modules/pkg.json": 30,
	}
	for name, size := range files {
		path := filepath.Join(root, name)
		data := make([]byte, size)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Create a symlink.
	os.Symlink(filepath.Join(root, "file1.go"), filepath.Join(root, "link1"))

	return root
}

func resultPaths(results []FindResult, root string) []string {
	var paths []string
	for _, r := range results {
		// Return paths relative to root for easier assertions.
		rel, _ := filepath.Rel(root, r.Path)
		paths = append(paths, rel)
	}
	return paths
}

func contains2(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func TestFindAll(t *testing.T) {
	root := setupFindFixture(t)
	results, err := RunFind(&FindParams{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	// Should find everything (dirs + files + symlink).
	if len(results) < 10 {
		t.Fatalf("expected at least 10 entries, got %d", len(results))
	}
}

func TestFindByName(t *testing.T) {
	root := setupFindFixture(t)
	results, err := RunFind(&FindParams{Path: root, Name: "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	paths := resultPaths(results, root)
	if len(paths) != 3 {
		t.Fatalf("expected 3 .go files, got %d: %v", len(paths), paths)
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, ".go") {
			t.Fatalf("unexpected file: %s", p)
		}
	}
}

func TestFindByTypeFile(t *testing.T) {
	root := setupFindFixture(t)
	results, err := RunFind(&FindParams{Path: root, Type: "f"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Type != "file" {
			t.Fatalf("expected only files, got %s (%s)", r.Type, r.Path)
		}
	}
}

func TestFindByTypeDir(t *testing.T) {
	root := setupFindFixture(t)
	results, err := RunFind(&FindParams{Path: root, Type: "d"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Type != "dir" {
			t.Fatalf("expected only dirs, got %s (%s)", r.Type, r.Path)
		}
	}
	paths := resultPaths(results, root)
	if !contains2(paths, "sub") {
		t.Fatal("expected 'sub' in directory results")
	}
	if !contains2(paths, "empty_dir") {
		t.Fatal("expected 'empty_dir' in directory results")
	}
}

func TestFindByTypeSymlink(t *testing.T) {
	root := setupFindFixture(t)
	results, err := RunFind(&FindParams{Path: root, Type: "l"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 symlink, got %d", len(results))
	}
	if results[0].Type != "symlink" {
		t.Fatalf("expected symlink type, got %s", results[0].Type)
	}
}

func TestFindMaxDepth(t *testing.T) {
	root := setupFindFixture(t)

	// Depth 1: should get root-level items + sub/ contents but not sub/deep/ contents.
	results, err := RunFind(&FindParams{Path: root, MaxDepth: 1, Name: "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	paths := resultPaths(results, root)
	// file1.go is at depth 1, sub/file3.go is at depth 2, sub/deep/file4.go at depth 3.
	// With maxdepth 1, only file1.go should match.
	if len(paths) != 1 {
		t.Fatalf("expected 1 .go file at maxdepth 1, got %d: %v", len(paths), paths)
	}
	if paths[0] != "file1.go" {
		t.Fatalf("expected file1.go, got %s", paths[0])
	}
}

func TestFindMaxDepth2(t *testing.T) {
	root := setupFindFixture(t)
	results, err := RunFind(&FindParams{Path: root, MaxDepth: 2, Name: "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	paths := resultPaths(results, root)
	// file1.go (depth 1) + sub/file3.go (depth 2).
	if len(paths) != 2 {
		t.Fatalf("expected 2 .go files at maxdepth 2, got %d: %v", len(paths), paths)
	}
}

func TestFindExclude(t *testing.T) {
	root := setupFindFixture(t)
	results, err := RunFind(&FindParams{
		Path:    root,
		Type:    "f",
		Exclude: []string{"node_modules", ".*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := resultPaths(results, root)
	for _, p := range paths {
		if strings.Contains(p, "node_modules") {
			t.Fatalf("node_modules should be excluded, got: %s", p)
		}
		if strings.HasPrefix(filepath.Base(p), ".") {
			t.Fatalf("hidden files should be excluded, got: %s", p)
		}
	}
}

func TestFindMtimeNewer(t *testing.T) {
	root := setupFindFixture(t)

	// All files were just created, so they're all "newer than 1 hour".
	results, err := RunFind(&FindParams{
		Path:  root,
		Type:  "f",
		Mtime: &MtimeFilter{OlderThan: false, Duration: 1 * time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected all recently-created files to match -1h")
	}

	// Nothing should be "newer than 0 minutes" (effectively in the future).
	// Actually everything is newer than 0... let's test "older than 0" which
	// should match everything.
	results, err = RunFind(&FindParams{
		Path:  root,
		Type:  "f",
		Mtime: &MtimeFilter{OlderThan: true, Duration: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	// All files have age > 0 (even if just milliseconds).
	if len(results) == 0 {
		t.Fatal("expected files older than 0 to match")
	}
}

func TestFindMtimeOlder(t *testing.T) {
	root := setupFindFixture(t)

	// Make one file "old" by backdating its mtime.
	oldFile := filepath.Join(root, "file1.go")
	oldTime := time.Now().Add(-48 * time.Hour)
	os.Chtimes(oldFile, oldTime, oldTime)

	results, err := RunFind(&FindParams{
		Path:  root,
		Type:  "f",
		Name:  "*.go",
		Mtime: &MtimeFilter{OlderThan: true, Duration: 24 * time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := resultPaths(results, root)
	if len(paths) != 1 || paths[0] != "file1.go" {
		t.Fatalf("expected only file1.go (backdated), got: %v", paths)
	}
}

func TestFindSizeLarger(t *testing.T) {
	root := setupFindFixture(t)
	results, err := RunFind(&FindParams{
		Path: root,
		Type: "f",
		Size: &SizeFilter{LargerThan: true, Bytes: 1000},
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := resultPaths(results, root)
	// Only file2.txt is 5000 bytes.
	if len(paths) != 1 || paths[0] != "file2.txt" {
		t.Fatalf("expected only file2.txt (5000 bytes), got: %v", paths)
	}
}

func TestFindSizeSmaller(t *testing.T) {
	root := setupFindFixture(t)
	results, err := RunFind(&FindParams{
		Path: root,
		Type: "f",
		Size: &SizeFilter{LargerThan: false, Bytes: 50},
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := resultPaths(results, root)
	// .hidden (10), node_modules/pkg.json (30) are < 50.
	for _, p := range paths {
		if p == "file1.go" || p == "file2.txt" || p == "sub/file3.go" || p == "sub/deep/file4.go" {
			t.Fatalf("file %s should not match size < 50", p)
		}
	}
}

func TestFindCombinedFilters(t *testing.T) {
	root := setupFindFixture(t)
	results, err := RunFind(&FindParams{
		Path:     root,
		Name:     "*.go",
		Type:     "f",
		MaxDepth: 2,
		Size:     &SizeFilter{LargerThan: true, Bytes: 50},
		Exclude:  []string{"node_modules"},
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := resultPaths(results, root)
	// file1.go (100, depth 1) + sub/file3.go (200, depth 2).
	// sub/deep/file4.go is excluded by maxdepth (depth 3).
	// file4.go is also only 50 bytes — not > 50.
	if len(paths) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(paths), paths)
	}
}

// --- Path validation tests ---

func TestValidateFindPath(t *testing.T) {
	tests := []struct {
		path      string
		extraDeny []string
		wantErr   bool
	}{
		// Hardcoded rejects
		{"/", nil, true},
		{"/proc", nil, true},
		{"/proc/1/status", nil, true},
		{"/sys", nil, true},
		{"/sys/class/power_supply", nil, true},
		{"/dev", nil, true},
		{"/dev/sda", nil, true},

		// Allowed (no longer hardcoded as denied)
		{"/bin", nil, false},
		{"/usr", nil, false},
		{"/etc", nil, false},
		{"/var", nil, false},
		{"/home", nil, false},
		{"/home/luser", nil, false},
		{"/tmp", nil, false},
		{"/tmp/foo", nil, false},
		{".", nil, false},
		{"./src", nil, false},
		{"relative/path", nil, false},
		{"/usr/local", nil, false},
		{"/var/log", nil, false},

		// Configurable deny list
		{"/usr", []string{"/usr", "/opt"}, true},
		{"/opt", []string{"/usr", "/opt"}, true},
		{"/usr/local", []string{"/usr", "/opt"}, false}, // subdirectory is fine
		{"/home/luser", []string{"/usr"}, false},        // not in deny list
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			err := ValidateFindPath(tt.path, tt.extraDeny)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for path %q", tt.path)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for path %q: %v", tt.path, err)
			}
		})
	}
}

// --- Parser tests ---

func TestParseMtime(t *testing.T) {
	tests := []struct {
		input     string
		olderThan bool
		duration  time.Duration
		wantErr   bool
	}{
		{"+7d", true, 7 * 24 * time.Hour, false},
		{"-1h", false, 1 * time.Hour, false},
		{"+30m", true, 30 * time.Minute, false},
		{"-2w", false, 14 * 24 * time.Hour, false},
		{"7d", false, 0, true},  // missing prefix
		{"+", false, 0, true},   // no value
		{"+7x", false, 0, true}, // unknown unit
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			f, err := ParseMtime(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if f.OlderThan != tt.olderThan {
				t.Fatalf("olderThan: got %v, want %v", f.OlderThan, tt.olderThan)
			}
			if f.Duration != tt.duration {
				t.Fatalf("duration: got %v, want %v", f.Duration, tt.duration)
			}
		})
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		input      string
		largerThan bool
		bytes      int64
		wantErr    bool
	}{
		{"+1M", true, 1024 * 1024, false},
		{"-10k", false, 10 * 1024, false},
		{"+500b", true, 500, false},
		{"-2G", false, 2 * 1024 * 1024 * 1024, false},
		{"+1024", true, 1024, false}, // no unit = bytes
		{"1M", false, 0, true},       // missing prefix
		{"+", false, 0, true},        // no value
		{"+1X", false, 0, true},      // unknown unit
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			f, err := ParseSize(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if f.LargerThan != tt.largerThan {
				t.Fatalf("largerThan: got %v, want %v", f.LargerThan, tt.largerThan)
			}
			if f.Bytes != tt.bytes {
				t.Fatalf("bytes: got %d, want %d", f.Bytes, tt.bytes)
			}
		})
	}
}

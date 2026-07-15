package main

import (
	"io"
	"strings"
	"testing"
)

func TestGrepFilter(t *testing.T) {
	input := "alpha\nbeta\ngamma\nalpha-two\ndelta"
	tests := []struct {
		name    string
		pattern string
		want    string
		wantErr bool
	}{
		{"simple match", "alpha", "alpha\nalpha-two", false},
		{"regex match", "^[bg]", "beta\ngamma", false},
		{"no match", "zzz", "", false},
		{"invalid regex", "[invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := newGrepReader(strings.NewReader(input), tt.pattern, false)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error from invalid pattern")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, err := readAll(r)
			if err != nil {
				t.Fatalf("read error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGrepVFilter(t *testing.T) {
	input := "error: disk full\ninfo: starting\nerror: timeout\ninfo: done\nwarn: slow"

	r, err := newGrepReader(strings.NewReader(input), "error", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := readAll(r)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	want := "info: starting\ninfo: done\nwarn: slow"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHeadFilter(t *testing.T) {
	input := "line1\nline2\nline3\nline4\nline5"
	tests := []struct {
		name  string
		count string
		want  string
	}{
		{"first 2", "2", "line1\nline2"},
		{"first 10 (more than available)", "10", "line1\nline2\nline3\nline4\nline5"},
		{"zero", "0", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := newHeadReader(strings.NewReader(input), tt.count)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, err := readAll(r)
			if err != nil {
				t.Fatalf("read error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTailFilter(t *testing.T) {
	input := "line1\nline2\nline3\nline4\nline5"
	tests := []struct {
		name  string
		count string
		want  string
	}{
		{"last 2", "2", "line4\nline5"},
		{"last 10 (more than available)", "10", "line1\nline2\nline3\nline4\nline5"},
		{"zero", "0", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := newTailReader(strings.NewReader(input), tt.count)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, err := readAll(r)
			if err != nil {
				t.Fatalf("read error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFilterChain(t *testing.T) {
	input := "error: disk full\ninfo: starting\nerror: timeout\ninfo: done\nwarn: slow"

	// grep error | head 1
	filters := []Filter{
		{Type: FilterGrep, Arg: "error"},
		{Type: FilterHead, Arg: "1"},
	}
	got, err := ApplyFilterStream(strings.NewReader(input), filters)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "error: disk full"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFilterChainGrepTail(t *testing.T) {
	input := "error: one\ninfo: two\nerror: three\ninfo: four\nerror: five"

	// grep error | tail 1
	filters := []Filter{
		{Type: FilterGrep, Arg: "error"},
		{Type: FilterTail, Arg: "1"},
	}
	got, err := ApplyFilterStream(strings.NewReader(input), filters)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "error: five"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestJQFilter(t *testing.T) {
	input := `{"name": "test", "count": 42}`

	r, err := newJQReader(strings.NewReader(input), ".name")
	if err != nil {
		t.Skipf("jq not available: %v", err)
	}
	got, err := readAll(r)
	if err != nil {
		t.Skipf("jq not available: %v", err)
	}
	want := `"test"`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestJQFilterArray(t *testing.T) {
	input := `[{"id":1,"x":"a"},{"id":2,"x":"b"}]`

	r, err := newJQReader(strings.NewReader(input), ".[].id")
	if err != nil {
		t.Skipf("jq not available: %v", err)
	}
	got, err := readAll(r)
	if err != nil {
		t.Skipf("jq not available: %v", err)
	}
	want := "1\n2"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// readAll reads an io.Reader to string, trimming trailing newline.
func readAll(r interface{}) (string, error) {
	reader, ok := r.(io.Reader)
	if !ok {
		return "", nil
	}
	b, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\n"), nil
}

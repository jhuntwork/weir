package main

import (
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   []string
		errMsg string
	}{
		{
			name:  "simple command",
			input: "git status",
			want:  []string{"git", "status"},
		},
		{
			name:  "single quoted arg",
			input: "git log '--format=%H %s'",
			want:  []string{"git", "log", "--format=%H %s"},
		},
		{
			name:  "double quoted arg",
			input: `git log "--format=%H %s"`,
			want:  []string{"git", "log", "--format=%H %s"},
		},
		{
			name:  "escaped quote in double quotes",
			input: `echo "say \"hello\""`,
			want:  []string{"echo", `say "hello"`},
		},
		{
			name:  "multiple spaces between args",
			input: "git   log   --oneline",
			want:  []string{"git", "log", "--oneline"},
		},
		{
			name:  "tabs as whitespace",
			input: "git\tlog",
			want:  []string{"git", "log"},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "only whitespace",
			input: "   \t  ",
			want:  nil,
		},
		{
			name:   "unterminated single quote",
			input:  "git log 'oops",
			errMsg: "unterminated single quote",
		},
		{
			name:   "unterminated double quote",
			input:  `git log "oops`,
			errMsg: "unterminated double quote",
		},
		{
			name:  "json arg in single quotes",
			input: `swamp model method run tracker list_issues --input '{"assignedToId": 1403}'`,
			want:  []string{"swamp", "model", "method", "run", "tracker", "list_issues", "--input", `{"assignedToId": 1403}`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tokenize(tt.input)
			if tt.errMsg != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errMsg)
				}
				if !contains(err.Error(), tt.errMsg) {
					t.Fatalf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !sliceEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringContains(s, sub))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func sliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

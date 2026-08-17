package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFinalResponseLimitRunsAfterFilters(t *testing.T) {
	input := strings.NewReader("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	output, truncated, err := ApplyFilterStreamWithLimits(input, []Filter{{Type: FilterGrep, Arg: "^8$"}}, 1, 1024, 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "8" || truncated {
		t.Fatalf("got output=%q truncated=%v, want output=8 without truncation", output, truncated)
	}
}

func TestFinalResponseLimitTruncatesOnlyTheResponse(t *testing.T) {
	result, err := ExecuteWithLimits(context.Background(), testConfig(), &Parsed{
		Command: "sh",
		Args:    []string{"-c", "seq 1 10"},
	}, 10, 3, 1024, 4096, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(result.Stdout); got != "1\n2\n3" {
		t.Fatalf("got %q, want first three lines", got)
	}
	if !result.Truncated {
		t.Fatal("expected final response truncation")
	}
}

func TestBufferedFilterFailsAtHardStreamBudget(t *testing.T) {
	_, _, err := ApplyFilterStreamWithLimits(strings.NewReader("1\n2\n3\n4\n5\n"), []Filter{{Type: FilterTail, Arg: "1"}}, 0, 0, 4)
	if !errors.Is(err, ErrStreamLimit) {
		t.Fatalf("got %v, want ErrStreamLimit", err)
	}
}

func TestHeadDrainsAndReportsHardStreamBudget(t *testing.T) {
	_, _, err := ApplyFilterStreamWithLimits(strings.NewReader("1\n2\n3\n4\n5\n"), []Filter{{Type: FilterHead, Arg: "1"}}, 0, 0, 4)
	if !errors.Is(err, ErrStreamLimit) {
		t.Fatalf("got %v, want ErrStreamLimit", err)
	}
}

func TestFindResultLimitAndCancellation(t *testing.T) {
	root := setupFindFixture(t)
	limited, err := RunFindWithLimits(context.Background(), &FindParams{Path: root}, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(limited.Results) != 2 || !limited.Truncated {
		t.Fatalf("got %#v, want two truncated results", limited)
	}

	cancelled, err := RunFindWithLimits(context.Background(), &FindParams{Path: t.TempDir()}, 1)
	if err != nil || len(cancelled.Results) != 1 || cancelled.Truncated {
		t.Fatalf("exact result cap should not truncate: result=%#v err=%v", cancelled, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = RunFindWithLimits(ctx, &FindParams{Path: root}, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

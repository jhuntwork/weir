package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ApplyFilterStream applies the filter chain to an io.Reader, returning
// the final output as a string. Filters are composed as a pipeline:
// each stage reads from the previous stage's output.
func ApplyFilterStream(r io.Reader, filters []Filter) (string, error) {
	current := r
	for _, f := range filters {
		next, err := buildStage(current, f)
		if err != nil {
			return "", fmt.Errorf("filter %s: %w", filterName(f.Type), err)
		}
		current = next
	}

	// Drain the final reader into a string.
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, current); err != nil {
		return "", fmt.Errorf("reading filter output: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// buildStage creates an io.Reader that reads from `input` and applies
// the given filter. For grep/head this is a streaming pipe; for jq it
// wires through the jq process; for tail it must buffer.
func buildStage(input io.Reader, f Filter) (io.Reader, error) {
	switch f.Type {
	case FilterGrep:
		return newGrepReader(input, f.Arg, false)
	case FilterGrepV:
		return newGrepReader(input, f.Arg, true)
	case FilterJQ:
		return newJQReader(input, f.Arg)
	case FilterHead:
		return newHeadReader(input, f.Arg)
	case FilterTail:
		return newTailReader(input, f.Arg)
	default:
		return nil, fmt.Errorf("unknown filter type")
	}
}

// --- Grep: streaming line-by-line filter ---

func newGrepReader(input io.Reader, pattern string, invert bool) (io.Reader, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	pr, pw := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			line := scanner.Text()
			match := re.MatchString(line)
			if match != invert {
				if _, err := fmt.Fprintln(pw, line); err != nil {
					pw.CloseWithError(err)
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.Close()
	}()
	return pr, nil
}

// --- JQ: pipe through jq process ---

func newJQReader(input io.Reader, expr string) (io.Reader, error) {
	jqPath, err := exec.LookPath("jq")
	if err != nil {
		return nil, fmt.Errorf("jq not found in PATH: %w", err)
	}

	cmd := exec.Command(jqPath, expr)
	cmd.Stdin = input

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("jq stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("jq start: %w", err)
	}

	// Wrap in a reader that waits for the process on EOF/close.
	return &jqReader{stdout: stdout, cmd: cmd, stderr: &stderr}, nil
}

type jqReader struct {
	stdout io.ReadCloser
	cmd    *exec.Cmd
	stderr *bytes.Buffer
	done   bool
}

func (j *jqReader) Read(p []byte) (int, error) {
	n, err := j.stdout.Read(p)
	if err == io.EOF && !j.done {
		j.done = true
		if waitErr := j.cmd.Wait(); waitErr != nil {
			errMsg := strings.TrimSpace(j.stderr.String())
			if errMsg != "" {
				return n, fmt.Errorf("jq: %s", errMsg)
			}
			return n, fmt.Errorf("jq: %w", waitErr)
		}
	}
	return n, err
}

// --- Head: streaming with early termination ---

func newHeadReader(input io.Reader, countStr string) (io.Reader, error) {
	n, err := strconv.Atoi(countStr)
	if err != nil {
		return nil, fmt.Errorf("invalid count: %w", err)
	}
	if n <= 0 {
		return strings.NewReader(""), nil
	}

	pr, pw := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(input)
		written := 0
		for scanner.Scan() && written < n {
			if _, err := fmt.Fprintln(pw, scanner.Text()); err != nil {
				pw.CloseWithError(err)
				return
			}
			written++
		}
		pw.Close()
	}()
	return pr, nil
}

// --- Tail: must buffer all input, then emit last N lines ---

func newTailReader(input io.Reader, countStr string) (io.Reader, error) {
	n, err := strconv.Atoi(countStr)
	if err != nil {
		return nil, fmt.Errorf("invalid count: %w", err)
	}
	if n <= 0 {
		return strings.NewReader(""), nil
	}

	// Ring buffer of last N lines.
	scanner := bufio.NewScanner(input)
	ring := make([]string, 0, n)
	pos := 0
	full := false

	for scanner.Scan() {
		line := scanner.Text()
		if !full {
			ring = append(ring, line)
			if len(ring) == n {
				full = true
			}
		} else {
			ring[pos] = line
			pos = (pos + 1) % n
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading input for tail: %w", err)
	}

	// Reconstruct in order.
	var result []string
	if full {
		result = append(result, ring[pos:]...)
		result = append(result, ring[:pos]...)
	} else {
		result = ring
	}
	return strings.NewReader(strings.Join(result, "\n")), nil
}

func filterName(t FilterType) string {
	switch t {
	case FilterGrep:
		return "grep"
	case FilterGrepV:
		return "grep_v"
	case FilterJQ:
		return "jq"
	case FilterHead:
		return "head"
	case FilterTail:
		return "tail"
	default:
		return "unknown"
	}
}

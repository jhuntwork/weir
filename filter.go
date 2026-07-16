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
	case FilterSort:
		return newSortReader(input, f.Arg)
	case FilterUniq:
		return newUniqReader(input, f.Arg)
	case FilterWC:
		return newWCReader(input, f.Arg)
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

// --- Sort: buffer all input, sort lines, emit ---

func newSortReader(input io.Reader, opts string) (io.Reader, error) {
	scanner := bufio.NewScanner(input)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading input for sort: %w", err)
	}

	reverse := false
	numeric := false
	for _, opt := range strings.Split(opts, ",") {
		switch strings.TrimSpace(opt) {
		case "-r", "r", "reverse":
			reverse = true
		case "-n", "n", "numeric":
			numeric = true
		case "":
			// no option
		default:
			return nil, fmt.Errorf("unknown sort option %q (allowed: reverse, numeric, or empty for lexicographic)", opt)
		}
	}

	if numeric {
		sortNumeric(lines, reverse)
	} else {
		sortLexicographic(lines, reverse)
	}

	return strings.NewReader(strings.Join(lines, "\n")), nil
}

func sortLexicographic(lines []string, reverse bool) {
	sortSliceStable(lines, func(i, j int) bool {
		if reverse {
			return lines[i] > lines[j]
		}
		return lines[i] < lines[j]
	})
}

func sortNumeric(lines []string, reverse bool) {
	sortSliceStable(lines, func(i, j int) bool {
		ni := parseLeadingNumber(lines[i])
		nj := parseLeadingNumber(lines[j])
		if reverse {
			return ni > nj
		}
		return ni < nj
	})
}

func parseLeadingNumber(s string) float64 {
	s = strings.TrimSpace(s)
	var numStr strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '+' {
			numStr.WriteRune(r)
		} else {
			break
		}
	}
	n, err := strconv.ParseFloat(numStr.String(), 64)
	if err != nil {
		return 0
	}
	return n
}

// sortSliceStable is a simple insertion sort — good enough for the
// typical <10k lines seen in command output. Avoids importing sort
// package for a single use in this file.
func sortSliceStable(s []string, less func(i, j int) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// --- Uniq: deduplicate adjacent identical lines ---

func newUniqReader(input io.Reader, opts string) (io.Reader, error) {
	count := false
	for _, opt := range strings.Split(opts, ",") {
		switch strings.TrimSpace(opt) {
		case "-c", "c", "count":
			count = true
		case "":
			// no option
		default:
			return nil, fmt.Errorf("unknown uniq option %q (allowed: count, or empty for plain dedup)", opt)
		}
	}

	scanner := bufio.NewScanner(input)
	var result []string
	var prev string
	n := 0
	first := true

	for scanner.Scan() {
		line := scanner.Text()
		if first {
			prev = line
			n = 1
			first = false
			continue
		}
		if line == prev {
			n++
		} else {
			if count {
				result = append(result, fmt.Sprintf("%7d %s", n, prev))
			} else {
				result = append(result, prev)
			}
			prev = line
			n = 1
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading input for uniq: %w", err)
	}
	// Flush last group.
	if !first {
		if count {
			result = append(result, fmt.Sprintf("%7d %s", n, prev))
		} else {
			result = append(result, prev)
		}
	}

	return strings.NewReader(strings.Join(result, "\n")), nil
}

// --- WC: count lines, words, or bytes ---

func newWCReader(input io.Reader, mode string) (io.Reader, error) {
	scanner := bufio.NewScanner(input)
	lines := 0
	words := 0
	chars := 0

	for scanner.Scan() {
		line := scanner.Text()
		lines++
		chars += len(line) + 1 // +1 for newline
		words += len(strings.Fields(line))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading input for wc: %w", err)
	}

	switch strings.TrimSpace(mode) {
	case "-l", "l", "lines", "":
		return strings.NewReader(strconv.Itoa(lines)), nil
	case "-w", "w", "words":
		return strings.NewReader(strconv.Itoa(words)), nil
	case "-c", "c", "chars", "bytes":
		return strings.NewReader(strconv.Itoa(chars)), nil
	default:
		return nil, fmt.Errorf("unknown wc mode %q (allowed: lines, words, chars)", mode)
	}
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
	case FilterSort:
		return "sort"
	case FilterUniq:
		return "uniq"
	case FilterWC:
		return "wc"
	default:
		return "unknown"
	}
}

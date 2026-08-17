package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const maxFilterLineBytes = 1 * 1024 * 1024

// ErrStreamLimit marks a hard resource budget breach. Callers must not return
// a plausible-looking partial result for filters that require complete input.
var ErrStreamLimit = errors.New("stream byte budget exceeded")

func newLineScanner(input io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), maxFilterLineBytes)
	return scanner
}

// byteLimitReader permits exactly limit bytes and then probes for one more byte
// so a stream that ends exactly at the limit remains valid.
type byteLimitReader struct {
	input     io.Reader
	remaining int
	limited   bool
}

func newByteLimitReader(input io.Reader, limit int) io.Reader {
	if limit <= 0 {
		return input
	}
	return &byteLimitReader{input: input, remaining: limit, limited: true}
}

func (r *byteLimitReader) Read(p []byte) (int, error) {
	if !r.limited {
		return r.input.Read(p)
	}
	if r.remaining == 0 {
		var probe [1]byte
		n, err := r.input.Read(probe[:])
		if n > 0 {
			return 0, ErrStreamLimit
		}
		return n, err
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.input.Read(p)
	r.remaining -= n
	return n, err
}

// outputCollector limits only the final response. It keeps draining its input
// after reaching a line/byte response limit so upstream pipe stages finish.
type outputCollector struct {
	output    bytes.Buffer
	maxLines  int
	maxBytes  int
	lines     int
	lineOpen  bool
	truncated bool
}

func (c *outputCollector) Write(p []byte) (int, error) {
	for _, b := range p {
		if c.truncated {
			continue
		}
		if c.maxBytes > 0 && c.output.Len() >= c.maxBytes {
			c.truncated = true
			continue
		}
		if c.maxLines > 0 && !c.lineOpen && c.lines >= c.maxLines {
			c.truncated = true
			continue
		}
		c.output.WriteByte(b)
		if !c.lineOpen {
			c.lines++
			c.lineOpen = true
		}
		if b == '\n' {
			c.lineOpen = false
		}
	}
	return len(p), nil
}

// CollectOutput drains input, retaining only the configured final response.
// Its caller must wrap input in byteLimitReader for a hard stream budget.
func CollectOutput(input io.Reader, maxLines, maxBytes int) (string, bool, error) {
	collector := &outputCollector{maxLines: maxLines, maxBytes: maxBytes}
	buf := make([]byte, 32*1024)
	for {
		n, err := input.Read(buf)
		if n > 0 {
			_, _ = collector.Write(buf[:n])
		}
		if err == io.EOF {
			return collector.output.String(), collector.truncated, nil
		}
		if err != nil {
			return collector.output.String(), collector.truncated, err
		}
	}
}

// ApplyFilterStream keeps the historic unbounded API for direct unit callers.
func ApplyFilterStream(input io.Reader, filters []Filter) (string, error) {
	output, _, err := ApplyFilterStreamWithLimits(input, filters, 0, 0, 0)
	return strings.TrimRight(output, "\n"), err
}

// ApplyFilterStreamWithLimits applies filters left-to-right and limits the
// response only after the final stage. Every pipeline edge has the hard stream
// byte budget so filters that buffer input cannot consume unbounded memory.
func ApplyFilterStreamWithLimits(input io.Reader, filters []Filter, maxLines, maxBytes, maxStreamBytes int) (string, bool, error) {
	var current io.Reader = newByteLimitReader(input, maxStreamBytes)
	for _, filter := range filters {
		next, err := buildStage(current, filter)
		if err != nil {
			return "", false, fmt.Errorf("filter %s: %w", filterName(filter.Type), err)
		}
		current = newByteLimitReader(next, maxStreamBytes)
	}
	output, truncated, err := CollectOutput(current, maxLines, maxBytes)
	if err != nil {
		return output, truncated, fmt.Errorf("reading filter output: %w", err)
	}
	return strings.TrimRight(output, "\n"), truncated, nil
}

func buildStage(input io.Reader, filter Filter) (io.Reader, error) {
	switch filter.Type {
	case FilterGrep:
		return newGrepReader(input, filter.Arg, false)
	case FilterGrepV:
		return newGrepReader(input, filter.Arg, true)
	case FilterJQ:
		return newJQReader(input, filter.Arg)
	case FilterHead:
		return newHeadReader(input, filter.Arg)
	case FilterTail:
		return newTailReader(input, filter.Arg)
	case FilterSort:
		return newSortReader(input, filter.Arg)
	case FilterUniq:
		return newUniqReader(input, filter.Arg)
	case FilterWC:
		return newWCReader(input, filter.Arg)
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
		scanner := newLineScanner(input)
		for scanner.Scan() {
			line := scanner.Text()
			match := re.MatchString(line)
			if match != invert {
				if _, err := fmt.Fprintln(pw, line); err != nil {
					_ = pw.CloseWithError(err)
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
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

// --- Head: stream the first N lines while continuing to drain input ---

func newHeadReader(input io.Reader, countStr string) (io.Reader, error) {
	n, err := strconv.Atoi(countStr)
	if err != nil {
		return nil, fmt.Errorf("invalid count: %w", err)
	}
	if n < 0 {
		n = 0
	}

	pr, pw := io.Pipe()
	go func() {
		scanner := newLineScanner(input)
		written := 0
		for scanner.Scan() {
			if written >= n {
				continue // Drain upstream like a completed pipe stage.
			}
			if _, err := fmt.Fprintln(pw, scanner.Text()); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			written++
		}
		if err := scanner.Err(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
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

	scanner := newLineScanner(input)
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

	var result []string
	if full {
		result = append(result, ring[pos:]...)
		result = append(result, ring[:pos]...)
	} else {
		result = ring
	}
	return strings.NewReader(strings.Join(result, "\n")), nil
}

// --- Sort: must buffer all input, then sort lines ---

func newSortReader(input io.Reader, opts string) (io.Reader, error) {
	scanner := newLineScanner(input)
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
	sort.SliceStable(lines, func(i, j int) bool {
		if reverse {
			return lines[i] > lines[j]
		}
		return lines[i] < lines[j]
	})
}

func sortNumeric(lines []string, reverse bool) {
	sort.SliceStable(lines, func(i, j int) bool {
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

// --- Uniq: deduplicate adjacent identical lines ---

func newUniqReader(input io.Reader, opts string) (io.Reader, error) {
	count := false
	for _, opt := range strings.Split(opts, ",") {
		switch strings.TrimSpace(opt) {
		case "-c", "c", "count":
			count = true
		case "":
		default:
			return nil, fmt.Errorf("unknown uniq option %q (allowed: count, or empty for plain dedup)", opt)
		}
	}

	scanner := newLineScanner(input)
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
	scanner := newLineScanner(input)
	lines := 0
	words := 0
	chars := 0
	for scanner.Scan() {
		line := scanner.Text()
		lines++
		chars += len(line) + 1
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

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FindParams holds the validated parameters for a find operation.
type FindParams struct {
	Path     string
	Name     string // glob pattern matched against basename
	Type     string // "f", "d", "l", or "" (any)
	MaxDepth int    // 0 means unlimited
	Mtime    *MtimeFilter
	Size     *SizeFilter
	Exclude  []string // glob patterns to skip
}

// MtimeFilter represents a relative age filter.
// Positive duration means "older than" (+7d), negative means "newer than" (-1h).
type MtimeFilter struct {
	OlderThan bool
	Duration  time.Duration
}

// SizeFilter represents a file size threshold.
// Positive means "larger than" (+1M), negative means "smaller than" (-10k).
type SizeFilter struct {
	LargerThan bool
	Bytes      int64
}

// FindResult is a single matched entry.
type FindResult struct {
	Path    string `json:"path"`
	Type    string `json:"type"` // "file", "dir", "symlink"
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// RunFind executes a find operation with the given parameters.
func RunFind(params *FindParams) ([]FindResult, error) {
	now := time.Now()
	baseDepth := strings.Count(filepath.Clean(params.Path), string(os.PathSeparator))

	var results []FindResult

	err := filepath.WalkDir(params.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip permission errors, continue walking.
			return nil
		}

		// Depth check.
		if params.MaxDepth > 0 {
			currentDepth := strings.Count(filepath.Clean(path), string(os.PathSeparator)) - baseDepth
			if currentDepth > params.MaxDepth {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}

		// Exclude patterns (matched against full path and basename).
		base := filepath.Base(path)
		for _, pattern := range params.Exclude {
			if matched, _ := filepath.Match(pattern, base); matched {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if matched, _ := filepath.Match(pattern, path); matched {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}

		// Type filter.
		if params.Type != "" {
			switch params.Type {
			case "f":
				if d.Type().IsDir() || d.Type()&fs.ModeSymlink != 0 {
					return nil
				}
			case "d":
				if !d.IsDir() {
					return nil
				}
			case "l":
				if d.Type()&fs.ModeSymlink == 0 {
					return nil
				}
			}
		}

		// Name filter (glob on basename).
		if params.Name != "" {
			matched, err := filepath.Match(params.Name, base)
			if err != nil {
				return fmt.Errorf("invalid name pattern: %w", err)
			}
			if !matched {
				return nil
			}
		}

		// Get file info for size/mtime checks.
		info, err := d.Info()
		if err != nil {
			return nil // skip files we can't stat
		}

		// Mtime filter.
		if params.Mtime != nil {
			age := now.Sub(info.ModTime())
			if params.Mtime.OlderThan && age < params.Mtime.Duration {
				return nil
			}
			if !params.Mtime.OlderThan && age > params.Mtime.Duration {
				return nil
			}
		}

		// Size filter (only meaningful for files).
		if params.Size != nil && !d.IsDir() {
			if params.Size.LargerThan && info.Size() <= params.Size.Bytes {
				return nil
			}
			if !params.Size.LargerThan && info.Size() >= params.Size.Bytes {
				return nil
			}
		}

		// Build result.
		typeName := "file"
		if d.IsDir() {
			typeName = "dir"
		} else if d.Type()&fs.ModeSymlink != 0 {
			typeName = "symlink"
		}

		results = append(results, FindResult{
			Path:    path,
			Type:    typeName,
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})

		return nil
	})

	return results, err
}

// ValidateFindPath checks that the path is safe to search.
// Hardcoded rejects: "/" and virtual/pseudo filesystems that are dangerous to walk.
// Additional paths can be denied via the extraDeny list (from --find-deny).
func ValidateFindPath(path string, extraDeny []string) error {
	cleaned := filepath.Clean(path)
	if cleaned == "/" {
		return fmt.Errorf("refusing to search from filesystem root — provide a specific directory")
	}
	// Hardcoded: virtual/pseudo filesystems where walking is dangerous or nonsensical.
	hardDeny := []string{"/proc", "/sys", "/dev"}
	for _, d := range hardDeny {
		if cleaned == d || strings.HasPrefix(cleaned, d+"/") {
			return fmt.Errorf("refusing to search %q — walking virtual filesystems is not safe", cleaned)
		}
	}
	// Configurable deny list.
	for _, d := range extraDeny {
		d = filepath.Clean(d)
		if cleaned == d {
			return fmt.Errorf("refusing to search %q — path is in the deny list", cleaned)
		}
	}
	return nil
}

// ParseMtime parses a relative time string like "+7d" or "-1h".
func ParseMtime(s string) (*MtimeFilter, error) {
	if len(s) < 2 {
		return nil, fmt.Errorf("mtime must be like '+7d' or '-1h'")
	}

	prefix := s[0]
	if prefix != '+' && prefix != '-' {
		return nil, fmt.Errorf("mtime must start with '+' (older than) or '-' (newer than)")
	}

	dur, err := parseRelativeDuration(s[1:])
	if err != nil {
		return nil, fmt.Errorf("invalid mtime %q: %w", s, err)
	}

	return &MtimeFilter{
		OlderThan: prefix == '+',
		Duration:  dur,
	}, nil
}

// ParseSize parses a size string like "+1M" or "-10k".
func ParseSize(s string) (*SizeFilter, error) {
	if len(s) < 2 {
		return nil, fmt.Errorf("size must be like '+1M' or '-10k'")
	}

	prefix := s[0]
	if prefix != '+' && prefix != '-' {
		return nil, fmt.Errorf("size must start with '+' (larger than) or '-' (smaller than)")
	}

	bytes, err := parseByteSize(s[1:])
	if err != nil {
		return nil, fmt.Errorf("invalid size %q: %w", s, err)
	}

	return &SizeFilter{
		LargerThan: prefix == '+',
		Bytes:      bytes,
	}, nil
}

// parseRelativeDuration parses "7d", "1h", "30m", "2w".
func parseRelativeDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("too short")
	}

	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %w", err)
	}

	switch unit {
	case 'm':
		return time.Duration(num) * time.Minute, nil
	case 'h':
		return time.Duration(num) * time.Hour, nil
	case 'd':
		return time.Duration(num) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(num) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown unit %q (use m, h, d, or w)", string(unit))
	}
}

// parseByteSize parses "1M", "10k", "500b", "2G".
func parseByteSize(s string) (int64, error) {
	if len(s) < 1 {
		return 0, fmt.Errorf("empty size")
	}

	unit := s[len(s)-1]
	var numStr string
	var multiplier int64

	switch unit {
	case 'b', 'B':
		numStr = s[:len(s)-1]
		multiplier = 1
	case 'k', 'K':
		numStr = s[:len(s)-1]
		multiplier = 1024
	case 'M':
		numStr = s[:len(s)-1]
		multiplier = 1024 * 1024
	case 'G':
		numStr = s[:len(s)-1]
		multiplier = 1024 * 1024 * 1024
	default:
		// No unit suffix — assume bytes.
		if unit >= '0' && unit <= '9' {
			numStr = s
			multiplier = 1
		} else {
			return 0, fmt.Errorf("unknown size unit %q (use b, k, K, M, or G)", string(unit))
		}
	}

	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %w", err)
	}
	return num * multiplier, nil
}

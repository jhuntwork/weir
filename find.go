package main

import (
	"context"
	"errors"
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
	Name     string
	Type     string
	MaxDepth int
	Mtime    *MtimeFilter
	Size     *SizeFilter
	Exclude  []string
}

type MtimeFilter struct {
	OlderThan bool
	Duration  time.Duration
}

type SizeFilter struct {
	LargerThan bool
	Bytes      int64
}

type FindResult struct {
	Path    string `json:"path"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// FindSearchResult preserves the completed portion of a deterministic walk and
// states whether Weir stopped at its configured response limit.
type FindSearchResult struct {
	Results   []FindResult
	Truncated bool
}

var errFindResultLimit = errors.New("find result limit reached")

// RunFind preserves the original unbounded API for direct callers and tests.
func RunFind(params *FindParams) ([]FindResult, error) {
	result, err := RunFindWithLimits(context.Background(), params, 0)
	return result.Results, err
}

// RunFindWithLimits walks a directory with cancellation and a result cap.
// A maxResults value of zero means unlimited.
func RunFindWithLimits(ctx context.Context, params *FindParams, maxResults int) (FindSearchResult, error) {
	now := time.Now()
	baseDepth := strings.Count(filepath.Clean(params.Path), string(os.PathSeparator))
	result := FindSearchResult{}

	err := filepath.WalkDir(params.Path, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil // Skip permission errors and continue walking.
		}

		if params.MaxDepth > 0 {
			currentDepth := strings.Count(filepath.Clean(path), string(os.PathSeparator)) - baseDepth
			if currentDepth > params.MaxDepth {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}

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

		if params.Name != "" {
			matched, err := filepath.Match(params.Name, base)
			if err != nil {
				return fmt.Errorf("invalid name pattern: %w", err)
			}
			if !matched {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		if params.Mtime != nil {
			age := now.Sub(info.ModTime())
			if params.Mtime.OlderThan && age < params.Mtime.Duration {
				return nil
			}
			if !params.Mtime.OlderThan && age > params.Mtime.Duration {
				return nil
			}
		}
		if params.Size != nil && !d.IsDir() {
			if params.Size.LargerThan && info.Size() <= params.Size.Bytes {
				return nil
			}
			if !params.Size.LargerThan && info.Size() >= params.Size.Bytes {
				return nil
			}
		}

		typeName := "file"
		if d.IsDir() {
			typeName = "dir"
		} else if d.Type()&fs.ModeSymlink != 0 {
			typeName = "symlink"
		}
		result.Results = append(result.Results, FindResult{
			Path: path, Type: typeName, Size: info.Size(), ModTime: info.ModTime().Format(time.RFC3339),
		})
		if maxResults > 0 && len(result.Results) > maxResults {
			result.Results = result.Results[:maxResults]
			result.Truncated = true
			return errFindResultLimit
		}
		return nil
	})
	if errors.Is(err, errFindResultLimit) {
		return result, nil
	}
	return result, err
}

// ValidateFindPath checks that the path is safe to search.
func ValidateFindPath(path string, extraDeny []string) error {
	cleaned := filepath.Clean(path)
	if cleaned == "/" {
		return fmt.Errorf("refusing to search from filesystem root — provide a specific directory")
	}
	hardDeny := []string{"/proc", "/sys", "/dev"}
	for _, denied := range hardDeny {
		if cleaned == denied || strings.HasPrefix(cleaned, denied+"/") {
			return fmt.Errorf("refusing to search %q — walking virtual filesystems is not safe", cleaned)
		}
	}
	for _, denied := range extraDeny {
		denied = filepath.Clean(denied)
		if cleaned == denied {
			return fmt.Errorf("refusing to search %q — path is in the deny list", cleaned)
		}
	}
	return nil
}

func ParseMtime(s string) (*MtimeFilter, error) {
	if len(s) < 2 {
		return nil, fmt.Errorf("mtime must be like '+7d' or '-1h'")
	}
	prefix := s[0]
	if prefix != '+' && prefix != '-' {
		return nil, fmt.Errorf("mtime must start with '+' (older than) or '-' (newer than)")
	}
	duration, err := parseRelativeDuration(s[1:])
	if err != nil {
		return nil, fmt.Errorf("invalid mtime %q: %w", s, err)
	}
	return &MtimeFilter{OlderThan: prefix == '+', Duration: duration}, nil
}

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
	return &SizeFilter{LargerThan: prefix == '+', Bytes: bytes}, nil
}

func parseRelativeDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("too short")
	}
	unit := s[len(s)-1]
	num, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
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

func parseByteSize(s string) (int64, error) {
	if len(s) < 1 {
		return 0, fmt.Errorf("empty size")
	}
	unit := s[len(s)-1]
	var numStr string
	var multiplier int64
	switch unit {
	case 'b', 'B':
		numStr, multiplier = s[:len(s)-1], 1
	case 'k', 'K':
		numStr, multiplier = s[:len(s)-1], 1024
	case 'M':
		numStr, multiplier = s[:len(s)-1], 1024*1024
	case 'G':
		numStr, multiplier = s[:len(s)-1], 1024*1024*1024
	default:
		if unit < '0' || unit > '9' {
			return 0, fmt.Errorf("unknown size unit %q (use b, k, K, M, or G)", string(unit))
		}
		numStr, multiplier = s, 1
	}
	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %w", err)
	}
	return num * multiplier, nil
}

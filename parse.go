package main

import (
	"fmt"
	"strings"
)

// Parsed is the internal representation passed to Execute.
type Parsed struct {
	Env     map[string]string
	Command string
	Args    []string
	Filters []Filter
}

// FilterType identifies which filter to apply.
type FilterType int

const (
	FilterGrep FilterType = iota
	FilterGrepV
	FilterJQ
	FilterHead
	FilterTail
)

// Filter is a single filter stage in the pipeline.
type Filter struct {
	Type FilterType
	Arg  string // pattern for grep, expression for jq, count for head/tail
}

// tokenize splits an input string into tokens, respecting single and
// double quotes. Used to split the command parameter into binary + args.
func tokenize(input string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	i := 0
	runes := []rune(input)

	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}

	for i < len(runes) {
		c := runes[i]
		switch {
		case c == ' ' || c == '\t':
			flush()
			i++
		case c == '\'':
			i++
			for i < len(runes) && runes[i] != '\'' {
				current.WriteRune(runes[i])
				i++
			}
			if i >= len(runes) {
				return nil, fmt.Errorf("unterminated single quote")
			}
			i++
		case c == '"':
			i++
			for i < len(runes) && runes[i] != '"' {
				if runes[i] == '\\' && i+1 < len(runes) {
					i++
					current.WriteRune(runes[i])
				} else {
					current.WriteRune(runes[i])
				}
				i++
			}
			if i >= len(runes) {
				return nil, fmt.Errorf("unterminated double quote")
			}
			i++
		default:
			current.WriteRune(c)
			i++
		}
	}
	flush()
	return tokens, nil
}

package rangespec

import (
	"errors"
	"strconv"
)

// Parse parses a single RFC 9110 byte-ranges header without applying resource length.
func Parse(header string) ([]Interval, error) {
	if len(header) < 5 || !equalFoldASCII(header[:5], "bytes") {
		return nil, SyntaxError{Offset: 0}
	}
	i := 5
	if i >= len(header) || header[i] != '=' {
		return nil, SyntaxError{Offset: i}
	}
	i++

	var intervals []Interval
	for {
		for i < len(header) && (header[i] == ' ' || header[i] == '\t') {
			i++
		}
		specStart := i
		if i >= len(header) || header[i] == ',' {
			return nil, SyntaxError{Offset: i}
		}

		if header[i] == '-' {
			i++
			n, next, ok := parseDigits(header, i, specStart)
			if !ok {
				return nil, SyntaxError{Offset: next}
			}
			i = next
			intervals = append(intervals, Interval{Start: -1, End: n})
		} else {
			first, next, ok := parseDigits(header, i, specStart)
			if !ok {
				return nil, SyntaxError{Offset: next}
			}
			i = next
			if i >= len(header) || header[i] != '-' {
				return nil, SyntaxError{Offset: i}
			}
			i++
			if i < len(header) && header[i] != ',' && header[i] != ' ' && header[i] != '\t' {
				last, next, ok := parseDigits(header, i, specStart)
				if !ok {
					return nil, SyntaxError{Offset: next}
				}
				i = next
				if last < first {
					return nil, SyntaxError{Offset: specStart}
				}
				intervals = append(intervals, Interval{Start: first, End: last})
			} else {
				intervals = append(intervals, Interval{Start: first, End: -1})
			}
		}

		for i < len(header) && (header[i] == ' ' || header[i] == '\t') {
			i++
		}
		if i == len(header) {
			break
		}
		if header[i] != ',' {
			return nil, SyntaxError{Offset: i}
		}
		i++
	}
	return intervals, nil
}

func parseDigits(s string, i int, specStart int) (int64, int, bool) {
	begin := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if begin == i {
		return 0, i, false
	}
	value, err := strconv.ParseUint(s[begin:i], 10, 63)
	if err != nil {
		return 0, specStart, false
	}
	return int64(value), i, true
}

func equalFoldASCII(s, want string) bool {
	if len(s) != len(want) {
		return false
	}
	for i := range s {
		left := s[i]
		if left >= 'A' && left <= 'Z' {
			left += 'a' - 'A'
		}
		right := want[i]
		if right >= 'A' && right <= 'Z' {
			right += 'a' - 'A'
		}
		if left != right {
			return false
		}
	}
	return true
}

// IsSyntaxError reports whether err is a Range-header syntax error.
func IsSyntaxError(err error) bool {
	var target SyntaxError
	return errors.As(err, &target)
}

// Package cronexpr parses a five-field cron expression into bit sets.
package cronexpr

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrFields    = errors.New("cronexpr: expected 5 fields")
	ErrRange     = errors.New("cronexpr: value out of range")
	ErrOrder     = errors.New("cronexpr: range start greater than end")
	ErrStep      = errors.New("cronexpr: step must be positive")
	ErrEmptyItem = errors.New("cronexpr: empty list item")
)

// FieldError wraps a sentinel with the 0-based field index and byte offset.
type FieldError struct {
	Err    error
	Field  int
	Offset int
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("%v (field %d, offset %d)", e.Err, e.Field, e.Offset)
}
func (e *FieldError) Unwrap() error { return e.Err }

// Expr is a parsed cron expression; each field is a 64-bit set.
type Expr struct {
	min  uint64 // bits 0-59
	hour uint64 // bits 0-23
	dom  uint64 // bits 1-31
	mon  uint64 // bits 1-12
	dow  uint64 // bits 0-6 (Sunday=0; 0 and 7 both map to 0)
	raw  string
}

func (e *Expr) Min() uint64    { return e.min }
func (e *Expr) Hour() uint64   { return e.hour }
func (e *Expr) Dom() uint64    { return e.dom }
func (e *Expr) Mon() uint64    { return e.mon }
func (e *Expr) Dow() uint64    { return e.dow }
func (e *Expr) String() string { return e.raw }

var names = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
	"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

// Parse compiles "min hour dom mon dow".
func Parse(s string) (*Expr, error) {
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return nil, ErrFields
	}
	max := [5]int{59, 23, 31, 12, 6}
	off := [5]int{}
	for i, p := 0, 0; i < 5; i++ {
		for p < len(s) && s[p] == ' ' {
			p++
		}
		off[i] = p
		p += len(fields[i])
	}
	var sets [5]uint64
	for i := 0; i < 5; i++ {
		b, err := parseField(fields[i], i, max[i], off[i])
		if err != nil {
			return nil, err
		}
		sets[i] = b
	}
	e := &Expr{sets[0], sets[1], sets[2], sets[3], sets[4], s}
	e.dow |= e.dow >> 7 // Sunday 7 -> 0
	e.dow &= 1<<7 - 1
	return e, nil
}

func parseField(f string, fi, max, off int) (uint64, error) {
	var bits uint64
	p := 0
	for _, item := range strings.Split(f, ",") {
		if item == "" {
			return 0, &FieldError{ErrEmptyItem, fi, off + p}
		}
		b, err := parseItem(item, fi, max, off+p)
		if err != nil {
			return 0, err
		}
		bits |= b
		p += len(item) + 1
	}
	return bits, nil
}

func parseItem(item string, fi, maxv, off int) (uint64, error) {
	step := 1
	body := item
	if k := strings.IndexByte(item, '/'); k >= 0 {
		n, ok := atoi(item[k+1:])
		if !ok {
			return 0, &FieldError{ErrRange, fi, off}
		}
		if n == 0 {
			return 0, &FieldError{ErrStep, fi, off + k + 1}
		}
		step, body = n, item[:k]
	}
	lo, hi, err := bounds(body, fi, maxv, off, strings.Contains(item, "/"))
	if err != nil {
		return 0, err
	}
	var bits uint64
	for v := lo; v <= hi; v += step {
		bits |= 1 << uint(v)
	}
	return bits, nil
}

func bounds(body string, fi, maxv, off int, hasSlash bool) (lo, hi int, err error) {
	if body == "*" {
		return 0, maxv, nil
	}
	if k := strings.IndexByte(body, '-'); k >= 0 {
		a, ok1 := value(body[:k], fi)
		b, ok2 := value(body[k+1:], fi)
		if !ok1 || !ok2 || a < 0 || b < 0 || a > maxv || b > maxv {
			return 0, 0, &FieldError{ErrRange, fi, off}
		}
		if a > b {
			return 0, 0, &FieldError{ErrOrder, fi, off}
		}
		return a, b, nil
	}
	v, ok := value(body, fi)
	if !ok || v < 0 || v > maxv {
		return 0, 0, &FieldError{ErrRange, fi, off}
	}
	if hasSlash { // a/n means a-max/n
		return v, maxv, nil
	}
	return v, v, nil
}

func value(tok string, fi int) (int, bool) {
	if n, ok := atoi(tok); ok {
		if fi == 4 && n == 7 {
			return 0, true
		}
		return n, true
	}
	if n, ok := names[strings.ToUpper(tok)]; ok {
		if (fi == 3 && n >= 1 && n <= 12) || (fi == 4 && n <= 6) {
			return n, true
		}
	}
	return 0, false
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

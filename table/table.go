package table

import "errors"

var ErrEmptyPattern = errors.New("empty pattern")

type Prefix struct {
	pattern string
	values  []int
}

func Build(pattern string) (*Prefix, error) {
	if pattern == "" {
		return nil, ErrEmptyPattern
	}
	values := make([]int, len(pattern)+1)
	for i := 2; i <= len(pattern); i++ {
		k := values[i-1]
		for k > 0 && pattern[i-1] != pattern[k] {
			k = values[k]
		}
		if pattern[i-1] == pattern[k] {
			k++
		}
		values[i] = k
	}
	return &Prefix{pattern: pattern, values: values}, nil
}

func (t *Prefix) Len() int { return len(t.pattern) }

func (t *Prefix) At(i int) int {
	if i < 1 || i > len(t.pattern) {
		return 0
	}
	return t.values[i]
}

func (t *Prefix) Pattern() string { return t.pattern }

func (t *Prefix) SelfCheck() bool {
	if t == nil || len(t.values) != len(t.pattern)+1 {
		return false
	}
	for i := 1; i <= len(t.pattern); i++ {
		v := t.values[i]
		if v < 0 || v >= i {
			return false
		}
		if t.pattern[:v] != t.pattern[i-v:i] {
			return false
		}
	}
	return true
}

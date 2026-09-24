// Package paramjoin joins continuation sections and decodes extended values.
package paramjoin

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"ontology/paramlex"
)

// Sentinels for errors.Is; details ride on the typed errors below.
var ErrIncomplete = errors.New("paramjoin: incomplete continuation")
var ErrCharset = errors.New("paramjoin: unsupported charset")
var ErrEncoding = errors.New("paramjoin: invalid encoding")

type IncompleteError struct{ Missing int } // 续行不完整：Missing 是缺失的段序号
type CharsetError struct{ Charset string } // 字符集不支持：Charset 是字符集名

func (e *IncompleteError) Error() string   { return fmt.Sprint(ErrIncomplete, " #", e.Missing) }
func (e *IncompleteError) Is(t error) bool { return t == ErrIncomplete }
func (e *CharsetError) Error() string      { return fmt.Sprint(ErrCharset, ": ", e.Charset) }
func (e *CharsetError) Is(t error) bool    { return t == ErrCharset }

type Param struct {
	Name, Value string
	Ext         bool
}

func Join(items []paramlex.Item) ([]Param, error) {
	byName := map[string][]paramlex.Item{}
	for _, it := range items {
		byName[it.Name] = append(byName[it.Name], it)
	}
	var out []Param
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		ps, err := joinGroup(name, byName[name])
		if err != nil {
			return nil, err
		}
		out = append(out, ps...)
	}
	return out, nil
}

func joinGroup(name string, group []paramlex.Item) ([]Param, error) {
	var pairs []Param
	secs, maxSec := map[int]paramlex.Item{}, -1
	for _, it := range group {
		if it.Section < 0 {
			pairs = append(pairs, Param{name, it.Value, it.Ext})
			continue
		}
		if _, dup := secs[it.Section]; dup {
			return nil, fmt.Errorf("%w: duplicate section %d", paramlex.ErrSyntax, it.Section)
		}
		secs[it.Section], maxSec = it, max(maxSec, it.Section)
	}
	if maxSec >= 0 {
		var b strings.Builder
		for s := 0; s <= maxSec; s++ {
			it, ok := secs[s]
			if !ok {
				return nil, &IncompleteError{Missing: s}
			}
			b.WriteString(it.Value)
		}
		pairs = append(pairs, Param{name, b.String(), secs[0].Ext})
	}
	for i, p := range pairs {
		if !p.Ext {
			continue
		}
		v, err := decodeExt(p.Value)
		if err != nil {
			return nil, err
		}
		pairs[i].Value = v
	}
	return pairs, nil
}

func decodeExt(v string) (string, error) {
	parts := strings.SplitN(v, "'", 3)
	if len(parts) < 3 {
		return "", fmt.Errorf("%w: ext value needs charset'lang' prefix", paramlex.ErrSyntax)
	}
	charset := strings.ToLower(parts[0])
	if charset != "utf-8" && charset != "iso-8859-1" {
		return "", &CharsetError{Charset: charset}
	}
	raw := make([]byte, 0, len(parts[2]))
	for i := 0; i < len(parts[2]); i++ {
		if parts[2][i] != '%' {
			raw = append(raw, parts[2][i])
			continue
		}
		n, err := strconv.ParseUint(parts[2][i+1:min(i+3, len(parts[2]))], 16, 8)
		if i+2 >= len(parts[2]) || err != nil {
			return "", fmt.Errorf("%w: bad percent escape", paramlex.ErrSyntax)
		}
		raw = append(raw, byte(n))
		i += 2
	}
	if charset == "iso-8859-1" {
		r := make([]rune, len(raw))
		for i, b := range raw {
			r[i] = rune(b)
		}
		return string(r), nil
	}
	if !utf8.Valid(raw) {
		return "", fmt.Errorf("%w: bytes are not valid utf-8", ErrEncoding)
	}
	return string(raw), nil
}

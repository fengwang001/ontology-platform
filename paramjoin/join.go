// Package paramjoin 把续行段拼接成完整参数值，并解码带扩展标记的值。
package paramjoin

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"ontology/paramlex"
)

var (
	ErrIncomplete = errors.New("paramjoin: incomplete continuation")
	ErrCharset    = errors.New("paramjoin: unsupported charset")
	ErrEncoding   = errors.New("paramjoin: invalid encoding")
)

// IncompleteError 续行不完整，Missing 为缺失的段序号。
type IncompleteError struct{ Missing int }

func (e *IncompleteError) Error() string { return fmt.Sprintf("missing segment %d", e.Missing) }
func (e *IncompleteError) Unwrap() error { return ErrIncomplete }

// CharsetError 字符集不支持，Charset 为头部给出的字符集名。
type CharsetError struct{ Charset string }

func (e *CharsetError) Error() string { return "unsupported charset " + e.Charset }
func (e *CharsetError) Unwrap() error { return ErrCharset }

// Param 是拼接解码后的完整参数。
type Param struct {
	Name  string
	Value string
	Ext   bool
}

// Join 按名字首次出现次序输出完整参数，同名续行段按序号升序直接相接。
func Join(items []paramlex.Item) ([]Param, error) {
	var order []string
	groups := map[string][]paramlex.Item{}
	for _, it := range items {
		if _, ok := groups[it.Name]; !ok {
			order = append(order, it.Name)
		}
		groups[it.Name] = append(groups[it.Name], it)
	}
	var out []Param
	for _, name := range order {
		base := len(out)
		var seqs []paramlex.Item
		for _, it := range groups[name] {
			if it.Seq >= 0 {
				seqs = append(seqs, it)
			} else {
				out = append(out, Param{Name: name, Value: it.Raw, Ext: it.Ext})
			}
		}
		if len(seqs) > 0 {
			sort.Slice(seqs, func(i, j int) bool { return seqs[i].Seq < seqs[j].Seq })
			var b strings.Builder
			for i, it := range seqs {
				if it.Seq != i {
					return nil, &IncompleteError{Missing: i}
				}
				b.WriteString(it.Raw)
			}
			out = append(out, Param{Name: name, Value: b.String(), Ext: seqs[0].Ext})
		}
		for i := base; i < len(out); i++ {
			if err := out[i].decode(); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// decode 解码带扩展标记的值：<字符集>'<语言>'<百分号编码的字节>。
func (p *Param) decode() error {
	if !p.Ext {
		return nil
	}
	parts := strings.SplitN(p.Value, "'", 3)
	if len(parts) < 3 {
		return fmt.Errorf("%w: ext value needs charset'lang'value", paramlex.ErrSyntax)
	}
	charset := strings.ToLower(parts[0])
	if charset != "utf-8" && charset != "iso-8859-1" {
		return &CharsetError{Charset: parts[0]}
	}
	raw, err := url.PathUnescape(parts[2])
	if err != nil {
		return fmt.Errorf("%w: %v", paramlex.ErrSyntax, err)
	}
	if charset == "iso-8859-1" {
		r := make([]rune, len(raw))
		for i, b := range []byte(raw) {
			r[i] = rune(b)
		}
		p.Value = string(r)
		return nil
	}
	if !utf8.ValidString(raw) {
		return fmt.Errorf("%w: not valid utf-8", ErrEncoding)
	}
	p.Value = raw
	return nil
}

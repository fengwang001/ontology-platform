// Package hoplex 解析逗号链式与键值式两种转发头部，产出统一的跳记录序列。
package hoplex

import "strings"

// Source 标记一条跳记录来自哪种头部，可按位或组合。
type Source uint8

const (
	Chain    Source = 1 << iota // 逗号链式（如 X-Forwarded-For）
	KeyValue                    // 键值式（如 Forwarded）
)

// Hop 是链路上的一跳；HasAddr 为 false 表示地址缺失（unknown 或混淆标识）。
type Hop struct {
	Source  Source
	Addr    string
	HasAddr bool
}

// ParseChain 解析逗号链式头部：最左最远端，空段忽略。
func ParseChain(header string) []Hop {
	var hops []Hop
	for _, part := range strings.Split(header, ",") {
		addr := strings.TrimSpace(part)
		if addr == "" {
			continue
		}
		hops = append(hops, Hop{Source: Chain, Addr: addr, HasAddr: !obfuscated(addr)})
	}
	return hops
}

// ParseForwarded 解析键值式头部：逗号分隔记录，分号分隔 key=value，只取 for 项。
func ParseForwarded(header string) []Hop {
	var hops []Hop
	for _, rec := range splitQuoted(header, ',') {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		hops = append(hops, parseRecord(rec))
	}
	return hops
}

// Parse 解析两种头部并按右对齐（近端对齐）拉链合并为统一跳序列：
// 同位置地址文本相同 ⇒ 去重并合并来源；不同 ⇒ 链式记录更远、键值记录更近。
func Parse(chainHeader, fwdHeader string) []Hop {
	a, b := ParseChain(chainHeader), ParseForwarded(fwdHeader)
	var tmp []Hop // 先按近端到远端收集
	i, j := len(a)-1, len(b)-1
	for i >= 0 || j >= 0 {
		switch {
		case i >= 0 && j >= 0 && sameText(a[i], b[j]):
			tmp = append(tmp, Hop{Source: a[i].Source | b[j].Source, Addr: a[i].Addr, HasAddr: a[i].HasAddr})
			i, j = i-1, j-1
		case i >= 0 && j >= 0:
			tmp = append(tmp, b[j], a[i])
			i, j = i-1, j-1
		case j >= 0:
			tmp = append(tmp, b[j])
			j--
		default:
			tmp = append(tmp, a[i])
			i--
		}
	}
	for l, r := 0, len(tmp)-1; l < r; l, r = l+1, r-1 {
		tmp[l], tmp[r] = tmp[r], tmp[l]
	}
	return tmp
}

func parseRecord(rec string) Hop {
	h := Hop{Source: KeyValue}
	for _, pair := range splitQuoted(rec, ';') {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "for") {
			continue
		}
		if v = unquote(strings.TrimSpace(v)); !obfuscated(v) {
			h.Addr, h.HasAddr = v, true
		}
	}
	return h
}

// splitQuoted 按分隔符切分，忽略双引号内的分隔符。
func splitQuoted(s string, sep byte) []string {
	var out []string
	start, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			quoted = !quoted
		case sep:
			if !quoted {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

func obfuscated(v string) bool {
	return v == "" || strings.EqualFold(v, "unknown") || strings.HasPrefix(v, "_")
}

func sameText(a, b Hop) bool {
	return a.HasAddr == b.HasAddr && (!a.HasAddr || strings.EqualFold(a.Addr, b.Addr))
}

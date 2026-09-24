// Package directive 解析缓存指令文本（Cache-Control 风格）。
//
// 文本是逗号分隔的项，每项为 name 或 name=delta；delta 为非负十进制
// 秒数，可加双引号。指令名大小写不敏感，统一存为小写。
package directive

import (
	"math"
	"strconv"
	"strings"
)

// Directive 表示一条已解析的指令。
type Directive struct {
	Present  bool  // 指令是否出现
	HasDelta bool  // 是否带合法的 delta 实参
	Delta    int64 // delta 秒数；溢出时取 int64 最大值
	Infinite bool  // 仅 max-stale：不带 delta，表示任意过期都接受
}

// Set 是指令名（小写）到指令的映射。
type Set map[string]Directive

// Parse 解析逗号分隔的指令文本。非法项整项忽略，不影响其他项；
// 同名指令只保留第一次出现。空文本返回空 Set。
func Parse(text string) Set {
	set := Set{}
	for _, item := range strings.Split(text, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, rawVal, hasEq := strings.Cut(item, "=")
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if _, exists := set[name]; exists {
			continue // 同名取第一次
		}
		if !hasEq {
			if needsDelta(name) {
				continue // 需要 delta 却缺失：整项忽略（max-stale 已先行特判）
			}
			d := Directive{Present: true}
			if name == "max-stale" {
				d.Infinite = true // 无 delta 的 max-stale：任意过期接受
			}
			set[name] = d
			continue
		}
		delta, ok := parseDelta(strings.TrimSpace(rawVal))
		if !ok {
			continue // 非法 delta：整项忽略
		}
		set[name] = Directive{Present: true, HasDelta: true, Delta: delta}
	}
	return set
}

// parseDelta 解析非负十进制整数；接受双引号包裹。
// 拒绝符号、小数点、非数字；溢出按 int64 最大值处理。
func parseDelta(raw string) (int64, bool) {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	for _, r := range raw {
		if r < '0' || r > '9' { // 顺带拒绝 +、-、小数点与空白
			return 0, false
		}
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return math.MaxInt64, true // 溢出：一个极大值
		}
		return 0, false
	}
	return n, true
}

// Has 返回某条指令是否出现（不论是否带 delta）。
func (s Set) Has(name string) bool {
	d, ok := s[strings.ToLower(name)]
	return ok && d.Present
}

// Get 取出某条指令。
func (s Set) Get(name string) (Directive, bool) {
	d, ok := s[strings.ToLower(name)]
	return d, ok
}

// needsDelta 报告该指令必须携带合法 delta；max-stale 不在此列。
func needsDelta(name string) bool {
	return name == "max-age" || name == "s-maxage" ||
		name == "min-fresh"
}

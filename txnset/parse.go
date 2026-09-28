package txnset

import (
	"math"
	"unicode"
	"unicode/utf8"
)

// MaxSourceLen 是来源标识的最大字节长度。
const MaxSourceLen = 32

// MaxIntervalCount 是单次 Parse（或 MergeText）允许的区间条目总数上限。
// 限制针对输入条目的数量，而非规范化后的区间数量。
const MaxIntervalCount = 10000

// 文本语法（不允许出现任何空白）：
//
//	集合    = 条目 *( "," 条目 ) / ""
//	条目    = 来源 ":" 事务号 [ "-" 事务号 ]
//	来源    = ( 字母 / "_" ) *( 字母 / 数字 / "_" )   ; 长度 <= MaxSourceLen 字节
//	事务号  = "0" / ( 非零数字 *数字 )                ; int64 范围内，无前导零
//
// 单个事务号表示单点 [N,N]，N-M 表示闭区间 [N,M]（N<=M）。
// 示例：src-a:1,src-a:2-4,src_b:10
//
// Parse 从左到右扫描并报告第一个错误；任一条目非法即整体失败，
// 不会并入任何前段条目。
func Parse(text string) (*TxnSet, error) {
	if text == "" {
		s := New()
		logParse(text, s.Canonical(), nil)
		return s, nil
	}
	entries, err := parseEntries(text)
	if err != nil {
		logParse(text, "", err)
		return nil, err
	}
	s := buildSet(entries)
	logParse(text, s.Canonical(), nil)
	return s, nil
}

type rawEntry struct {
	source string
	iv     Interval
}

// parseEntries 按逗号切分并逐条解析，offset 均为 text 中的字节偏移。
func parseEntries(text string) ([]rawEntry, error) {
	entries := make([]rawEntry, 0, min(len(text), 16))
	start := 0
	i := 0
	for i <= len(text) {
		if i < len(text) && text[i] != ',' {
			i++
			continue
		}
		if len(entries) >= MaxIntervalCount {
			return nil, newParseError(KindTooManyIntervals, start,
				"interval count exceeds MaxIntervalCount")
		}
		body := text[start:i]
		if body == "" {
			return nil, newParseError(KindSyntax, start, "empty entry")
		}
		e, err := parseEntry(body, start)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
		i++
		start = i
	}
	return entries, nil
}

// parseEntry 解析单个条目；base 为该条目在全文中的字节起点。
func parseEntry(body string, base int) (rawEntry, error) {
	// 扫描来源标识直到 ':'。
	byteLen := 0
	p := 0
	for p < len(body) {
		r, size := utf8.DecodeRuneInString(body[p:])
		if r == ':' {
			break
		}
		if unicode.IsSpace(r) {
			return rawEntry{}, newParseError(KindSyntax, base+p,
				"whitespace is not allowed")
		}
		if byteLen+size > MaxSourceLen {
			return rawEntry{}, newParseError(KindIdentifier, base+p,
				"source identifier exceeds MaxSourceLen bytes")
		}
		ok := r == '_' || unicode.IsLetter(r)
		if byteLen > 0 {
			ok = ok || unicode.IsDigit(r)
		}
		if !ok {
			return rawEntry{}, newParseError(KindIdentifier, base+p,
				"illegal character in source identifier")
		}
		byteLen += size
		p += size
	}
	if p == len(body) {
		return rawEntry{}, newParseError(KindSyntax, base+p,
			"missing ':' in entry")
	}
	if byteLen == 0 {
		return rawEntry{}, newParseError(KindSyntax, base,
			"empty source identifier")
	}
	source := body[:p]

	iv, err := parseBounds(body[p+1:], base+p+1)
	if err != nil {
		return rawEntry{}, err
	}
	return rawEntry{source: source, iv: iv}, nil
}

// parseBounds 解析冒号之后的 "N" 或 "N-M"；base 为该子串的绝对字节偏移。
// 单遍从左到右扫描，错误偏移即首次发现非法构造的位置。
func parseBounds(s string, base int) (Interval, error) {
	if s == "" {
		return Interval{}, newParseError(KindSyntax, base,
			"missing transaction number")
	}
	if s[0] == '-' {
		return Interval{}, newParseError(KindSyntax, base,
			"missing lower bound before '-'")
	}
	lo, n, err := parseNumber(s, 0, base)
	if err != nil {
		return Interval{}, err
	}
	if n == len(s) {
		return Interval{Lo: lo, Hi: lo}, nil
	}
	switch s[n] {
	case '-':
		if n+1 == len(s) {
			return Interval{}, newParseError(KindSyntax, base+len(s),
				"missing upper bound after '-'")
		}
	default:
		if isWhitespaceAt(s, n) {
			return Interval{}, newParseError(KindSyntax, base+n,
				"whitespace is not allowed")
		}
		return Interval{}, newParseError(KindNumber, base+n,
			"illegal character in transaction number")
	}
	hi, m, err := parseNumber(s, n+1, base)
	if err != nil {
		return Interval{}, err
	}
	if m != len(s) {
		if s[m] == '-' {
			return Interval{}, newParseError(KindSyntax, base+m,
				"multiple '-' in interval")
		}
		if isWhitespaceAt(s, m) {
			return Interval{}, newParseError(KindSyntax, base+m,
				"whitespace is not allowed")
		}
		return Interval{}, newParseError(KindNumber, base+m,
			"illegal character in transaction number")
	}
	if lo > hi {
		return Interval{}, newParseError(KindNumber, base+n+1,
			"lower bound greater than upper bound")
	}
	return Interval{Lo: lo, Hi: hi}, nil
}

// parseNumber 从 s[i] 起读取一段十进制无符号整数，返回数值与结束下标。
// 拒绝前导零与超出 int64 的数值；错误偏移指向该数字 token 的起点。
func parseNumber(s string, i, base int) (int64, int, error) {
	if i >= len(s) || s[i] < '0' || s[i] > '9' {
		if i < len(s) && isWhitespaceAt(s, i) {
			return 0, i, newParseError(KindSyntax, base+i,
				"whitespace is not allowed")
		}
		return 0, i, newParseError(KindNumber, base+i,
			"illegal character in transaction number")
	}
	start := i
	if s[i] == '0' {
		i++
		if i < len(s) && s[i] >= '0' && s[i] <= '9' {
			return 0, start, newParseError(KindNumber, base+start,
				"leading zero in transaction number")
		}
		return 0, i, nil
	}
	var n int64
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		d := int64(s[i] - '0')
		if n > (math.MaxInt64-d)/10 {
			return 0, start, newParseError(KindNumber, base+start,
				"transaction number overflows int64")
		}
		n = n*10 + d
		i++
	}
	return n, i, nil
}

// isWhitespaceAt 判断 s[i] 处的字符是否为 Unicode 空白（含全角空格等
// 非 ASCII 空白）；i 必须是合法的字符边界。
func isWhitespaceAt(s string, i int) bool {
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r != utf8.RuneError && unicode.IsSpace(r)
}

// buildSet 把原始条目按来源分组并规范化为新集合。
func buildSet(entries []rawEntry) *TxnSet {
	m := make(map[string][]Interval)
	for _, e := range entries {
		m[e.source] = append(m[e.source], e.iv)
	}
	for src, ivs := range m {
		m[src] = normalizeIntervals(ivs)
	}
	return &TxnSet{m: m}
}

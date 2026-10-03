package canon

import (
	"bytes"
	"sort"
	"strings"
	"sync/atomic"
)

var headerVisits uint64

// Pair 是一个有序的查询参数或请求头（名, 值）。
type Pair struct {
	Name  string
	Value string
}

var (
	// ErrInvalidParam 表示参数非法（method/path 不合法）。
	ErrInvalidParam = errInvalidParam{}
	// ErrMissingSignedHeader 表示签名头缺失（不含 host/x-date 或请求中无此头）。
	ErrMissingSignedHeader = errMissingSignedHeader{}
)

type errInvalidParam struct{}

func (errInvalidParam) Error() string { return "canon: invalid parameter" }

type errMissingSignedHeader struct{}

func (errMissingSignedHeader) Error() string { return "canon: missing signed header" }

// HeaderVisits 返回规范化逻辑对请求头的累计访问次数（测试用）。
func HeaderVisits() uint64 { return atomic.LoadUint64(&headerVisits) }

const hexUpper = "0123456789ABCDEF"

// encodeBytes 按规则编码：unreserved 原样，其余每字节 %XX（大写）。
// keepSlash 为 true 时 "/" 也保留（用于 path）。
func encodeBytes(s string, keepSlash bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-' || c == '.' || c == '_' || c == '~':
			b.WriteByte(c)
		case c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexUpper[c>>4])
			b.WriteByte(hexUpper[c&0x0f])
		}
	}
	return b.String()
}

// foldValue 去首尾空白，并把内部连续的空格/制表符折成一个空格。
func foldValue(v string) string {
	v = strings.TrimSpace(v)
	var b strings.Builder
	b.Grow(len(v))
	inGap := false
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == ' ' || c == '\t' {
			if !inGap {
				b.WriteByte(' ')
				inGap = true
			}
			continue
		}
		inGap = false
		b.WriteByte(c)
	}
	return b.String()
}

// FoldHeaderValue 导出供 verify 折叠 x-date 头（不计入 headerVisits）。
func FoldHeaderValue(v string) string { return foldValue(v) }

// Build 按 KS4 规则构造规范请求。
func Build(method string, path string, query []Pair, headers []Pair, signedNames []string, payloadHash string) (string, error) {
	atomic.StoreUint64(&headerVisits, 0)

	// 参数校验：method 须为非空大写 ASCII 字母；path 须以 "/" 开头。
	if method == "" {
		return "", ErrInvalidParam
	}
	for i := 0; i < len(method); i++ {
		c := method[i]
		if !(c >= 'A' && c <= 'Z') {
			return "", ErrInvalidParam
		}
	}
	if len(path) == 0 || path[0] != '/' {
		return "", ErrInvalidParam
	}

	// 第一步：顺序扫描全部请求头（恰 H 次访问），按小写名折叠入 map，
	// 同时记录每个名字第一次出现的位置用于稳定遍历。
	folded := make(map[string][]string, len(headers))
	order := make([]string, 0, len(headers))
	for _, h := range headers {
		atomic.AddUint64(&headerVisits, 1)
		name := strings.ToLower(h.Name)
		if _, seen := folded[name]; !seen {
			order = append(order, name)
		}
		folded[name] = append(folded[name], foldValue(h.Value))
	}

	// 签名头名：转小写、去重、字节序升序（恰 S 次额外访问）。
	seen := make(map[string]bool, len(signedNames))
	names := make([]string, 0, len(signedNames))
	for _, n := range signedNames {
		ln := strings.ToLower(n)
		if !seen[ln] {
			seen[ln] = true
			names = append(names, ln)
		}
	}
	sort.Strings(names)

	// 缺签名头：必须含 host 与 x-date，且每个名字在请求中至少出现一次。
	if !seen["host"] || !seen["x-date"] {
		return "", ErrMissingSignedHeader
	}
	for _, n := range names {
		atomic.AddUint64(&headerVisits, 1)
		if len(folded[n]) == 0 {
			return "", ErrMissingSignedHeader
		}
	}

	// 查询串：名/值分别编码后按（编码名，编码值）升序。
	type encPair struct{ n, v string }
	eq := make([]encPair, len(query))
	for i, q := range query {
		eq[i] = encPair{encodeBytes(q.Name, false), encodeBytes(q.Value, false)}
	}
	sort.SliceStable(eq, func(i, j int) bool {
		if eq[i].n != eq[j].n {
			return eq[i].n < eq[j].n
		}
		return eq[i].v < eq[j].v
	})

	var b bytes.Buffer
	b.WriteString(method)
	b.WriteByte('\n')
	b.WriteString(encodeBytes(path, true))
	b.WriteByte('\n')
	for i, p := range eq {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p.n)
		b.WriteByte('=')
		b.WriteString(p.v)
	}
	b.WriteByte('\n')
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte(':')
		b.WriteString(strings.Join(folded[n], ","))
		b.WriteByte('\n')
	}
	b.WriteString(strings.Join(names, ";"))
	b.WriteByte('\n')
	b.WriteString(payloadHash)
	return b.String(), nil
}

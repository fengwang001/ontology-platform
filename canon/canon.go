package canon

import (
	"bytes"
	"errors"
	"sort"
)

// Header 是一个请求头：Name 大小写不敏感，Value 为原始文本。
type Header struct {
	Name  string
	Value string
}

// Build 构造规范请求时可能返回的参数级错误。
var (
	ErrInvalidArg    = errors.New("参数非法")
	ErrMissingHeader = errors.New("缺签名头")
)

// headerVisits 为非导出计数器，记录最近一次 Build 对请求头的访问总数。
var headerVisits int

const hexUpper = "0123456789ABCDEF"

func validMethod(method string) bool {
	if method == "" {
		return false
	}
	for i := 0; i < len(method); i++ {
		c := method[i]
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// encode 按字节百分号编码；keepSlash 仅用于 path 额外保留 "/"。
func encode(src string, keepSlash bool) string {
	var buf bytes.Buffer
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			buf.WriteByte(c)
		case c == '-' || c == '.' || c == '_' || c == '~':
			buf.WriteByte(c)
		case keepSlash && c == '/':
			buf.WriteByte(c)
		default:
			buf.WriteByte('%')
			buf.WriteByte(hexUpper[c>>4])
			buf.WriteByte(hexUpper[c&0x0f])
		}
	}
	return buf.String()
}

func buildQuery(query [][2]string) string {
	encoded := make([][2]string, len(query))
	for i, p := range query {
		encoded[i] = [2]string{encode(p[0], false), encode(p[1], false)}
	}
	sort.SliceStable(encoded, func(i, j int) bool {
		if encoded[i][0] != encoded[j][0] {
			return encoded[i][0] < encoded[j][0]
		}
		return encoded[i][1] < encoded[j][1]
	})
	var buf bytes.Buffer
	for i, p := range encoded {
		if i > 0 {
			buf.WriteByte('&')
		}
		buf.WriteString(p[0])
		buf.WriteByte('=')
		buf.WriteString(p[1])
	}
	return buf.String()
}

// foldValue 去首尾空白并把内部连续空格/制表符折成一个空格。
func foldValue(v string) string {
	out := make([]byte, 0, len(v))
	lastSpace := true
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == ' ' || c == '\t' {
			if !lastSpace {
				out = append(out, ' ')
				lastSpace = true
			}
			continue
		}
		out = append(out, c)
		lastSpace = false
	}
	for len(out) > 0 && out[len(out)-1] == ' ' {
		out = out[:len(out)-1]
	}
	return string(out)
}

// signedHeaderNames 转小写、去重并按字节序升序。
func signedHeaderNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		ln := lower(n)
		if _, ok := seen[ln]; ok {
			continue
		}
		seen[ln] = struct{}{}
		out = append(out, ln)
	}
	sort.Strings(out)
	return out
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// Build 按规范构造请求字符串。
func Build(method, path string, query [][2]string, headers []Header, signedNames []string, payloadHash string) (string, error) {
	headerVisits = 0
	if !validMethod(method) || len(path) == 0 || path[0] != '/' {
		return "", ErrInvalidArg
	}

	signed := signedHeaderNames(signedNames)
	required := map[string]bool{"host": false, "x-date": false}
	for _, n := range signed {
		if _, ok := required[n]; ok {
			required[n] = true
		}
	}
	for _, present := range required {
		if !present {
			return "", ErrMissingHeader
		}
	}

	// 一次线性扫描完成分组：每读一条请求头计一次，未命中的签名名补计一次。
	values := make([][]string, len(signed))
	matched := make([]bool, len(signed))
	for _, h := range headers {
		headerVisits++
		ln := lower(h.Name)
		i := sort.SearchStrings(signed, ln)
		if i < len(signed) && signed[i] == ln {
			values[i] = append(values[i], foldValue(h.Value))
			matched[i] = true
		}
	}
	for _, ok := range matched {
		if !ok {
			headerVisits++
			return "", ErrMissingHeader
		}
	}

	var buf bytes.Buffer
	buf.WriteString(method)
	buf.WriteByte('\n')
	buf.WriteString(encode(path, true))
	buf.WriteByte('\n')
	buf.WriteString(buildQuery(query))
	buf.WriteByte('\n')
	for i, name := range signed {
		buf.WriteString(name)
		buf.WriteByte(':')
		for j, v := range values[i] {
			if j > 0 {
				buf.WriteByte(',')
			}
			buf.WriteString(v)
		}
		buf.WriteByte('\n')
	}
	for i, name := range signed {
		if i > 0 {
			buf.WriteByte(';')
		}
		buf.WriteString(name)
	}
	buf.WriteByte('\n')
	buf.WriteString(payloadHash)
	return buf.String(), nil
}

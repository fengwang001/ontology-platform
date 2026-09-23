package sizeline

import (
	"errors"
	"strconv"
)

var (
	errBadSize      = errors.New("sizeline: bad size field")
	errBadExt       = errors.New("sizeline: bad extension")
	errUnterminated = errors.New("sizeline: unterminated quoted value")
)

// ParseLine 解析一行块大小行（不含结尾 CRLF），返回块大小与扩展。
// 例如 "8;name=abc;q=\"a;b=c\"" -> 8, [name=abc q=a;b=c]。
func ParseLine(line []byte) (int, []Ext, error) {
	i := 0
	for i < len(line) && line[i] != ';' {
		i++
	}
	if i == 0 {
		return 0, nil, errBadSize
	}
	size, err := strconv.ParseInt(string(line[:i]), 16, 64)
	if err != nil || size < 0 {
		return 0, nil, errBadSize
	}
	var exts []Ext
	for i < len(line) {
		i++ // 跳过 ';'
		j := i
		for j < len(line) && line[j] != '=' && line[j] != ';' {
			j++
		}
		if j >= len(line) || line[j] != '=' || j == i {
			return 0, nil, errBadExt
		}
		key := string(line[i:j])
		i = j + 1
		val, next, err := parseValue(line, i)
		if err != nil {
			return 0, nil, err
		}
		exts = append(exts, Ext{Key: key, Value: val})
		i = next
	}
	return int(size), exts, nil
}

// parseValue 从 line[i] 起解析裸值或带引号值，返回值与下一个分号位置。
func parseValue(line []byte, i int) (string, int, error) {
	if i >= len(line) || line[i] != '"' {
		j := i
		for j < len(line) && line[j] != ';' {
			j++
		}
		return string(line[i:j]), j, nil
	}
	i++ // 跳过开引号
	var buf []byte
	for {
		if i >= len(line) {
			return "", 0, errUnterminated
		}
		switch c := line[i]; {
		case c == '"':
			i++
			if i < len(line) && line[i] != ';' {
				return "", 0, errBadExt
			}
			return string(buf), i, nil
		case c == '\\':
			i++
			if i >= len(line) {
				return "", 0, errUnterminated
			}
			switch line[i] {
			case 'r':
				buf = append(buf, '\r')
			case 'n':
				buf = append(buf, '\n')
			default:
				buf = append(buf, line[i])
			}
			i++
		default:
			// 引号内的 ';' 与 '=' 都是值的字面量，不参与结构解析。
			buf = append(buf, c)
			i++
		}
	}
}

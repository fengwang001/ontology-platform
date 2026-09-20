package headers

import (
	"fmt"
	"strings"
)

// Parse 把原始头部文本解析成有序的键值集合。
// single 列出只允许出现一次的头名（大小写不敏感）：
// 这些头出现多次时取值完全相同则去重，不同则返回 ErrSingleValue。
// 出错时返回的 *Set 为 nil。
func Parse(raw string, single []string) (*Set, error) {
	lines, err := splitLines(raw)
	if err != nil {
		return nil, err
	}

	s := newSet()
	last := -1 // 上一个头在 s.names 中的下标，用于续行拼接
	for _, line := range lines {
		if line[0] == ' ' || line[0] == '\t' {
			if last < 0 {
				return nil, fmt.Errorf("%w: leading continuation line", ErrMalformed)
			}
			fold(s, last, line)
			continue
		}
		name, value, err := parseField(line)
		if err != nil {
			return nil, err
		}
		s.add(name, value)
		last = indexOf(s.names, name)
	}

	if err := s.dedupeSingles(single); err != nil {
		return nil, err
	}
	return s, nil
}

// splitLines 按 CRLF 切分原始文本，任何不符合 `\r\n` 的行尾都是语法错误。
func splitLines(raw string) ([]string, error) {
	var lines []string
	rest := raw
	for len(rest) > 0 {
		i := strings.IndexByte(rest, '\n')
		if i < 0 || i == 0 || rest[i-1] != '\r' {
			return nil, fmt.Errorf("%w: line not terminated by CRLF", ErrMalformed)
		}
		line := rest[:i-1]
		if strings.IndexByte(line, '\r') >= 0 {
			return nil, fmt.Errorf("%w: bare CR in line", ErrMalformed)
		}
		lines = append(lines, line)
		rest = rest[i+1:]
	}
	return lines, nil
}

// parseField 解析单行 "Name: value"，返回规范化键名与修剪后的值。
func parseField(line string) (string, string, error) {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		return "", "", fmt.Errorf("%w: missing colon in %q", ErrMalformed, line)
	}
	name := line[:i]
	if !validName(name) {
		return "", "", fmt.Errorf("%w: invalid header name %q", ErrMalformed, name)
	}
	value := strings.Trim(line[i+1:], " \t")
	return Canonical(name), strings.Clone(value), nil
}

// fold 把续行拼接到上一个头的值上：续行空白压成一个空格，去掉原值尾随空白。
func fold(s *Set, last int, line string) {
	name := s.names[last]
	vs := s.values[name]
	prev := strings.TrimRight(vs[len(vs)-1], " \t")
	cont := strings.Trim(line, " \t")
	if prev == "" {
		vs[len(vs)-1] = cont
	} else {
		vs[len(vs)-1] = prev + " " + cont
	}
}

// dedupeSingles 校验单值头：重复出现且取值不同则报错，相同则只保留一个。
func (s *Set) dedupeSingles(single []string) error {
	for _, name := range single {
		key := Canonical(name)
		vs, ok := s.values[key]
		if !ok || len(vs) <= 1 {
			continue
		}
		for _, v := range vs[1:] {
			if v != vs[0] {
				return fmt.Errorf("%w: %q", ErrSingleValue, key)
			}
		}
		s.values[key] = vs[:1]
	}
	return nil
}

func indexOf(names []string, name string) int {
	for i, n := range names {
		if n == name {
			return i
		}
	}
	return -1
}

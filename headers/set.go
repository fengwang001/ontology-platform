package headers

import "strings"

// Set 是有序的头部键值集合，键名查找不区分大小写。
type Set struct {
	names  []string            // 规范化后的键名，按首次出现顺序
	values map[string][]string // 规范化键名 -> 按出现顺序的值
}

func newSet() *Set {
	return &Set{values: make(map[string][]string)}
}

// add 追加一个键值对，name 必须已规范化。
func (s *Set) add(name, value string) {
	if _, ok := s.values[name]; !ok {
		s.names = append(s.names, name)
	}
	s.values[name] = append(s.values[name], value)
}

// Get 返回指定头的值；多值头用 ", " 拼接。第二个返回值表示头是否存在。
func (s *Set) Get(name string) (string, bool) {
	vs, ok := s.values[Canonical(name)]
	if !ok {
		return "", false
	}
	return strings.Join(vs, ", "), true
}

// Values 按出现顺序返回指定头的所有值。
func (s *Set) Values(name string) []string {
	vs, ok := s.values[Canonical(name)]
	if !ok {
		return nil
	}
	out := make([]string, len(vs))
	copy(out, vs)
	return out
}

// Names 返回规范化后的键名，按首次出现顺序。
func (s *Set) Names() []string {
	out := make([]string, len(s.names))
	copy(out, s.names)
	return out
}

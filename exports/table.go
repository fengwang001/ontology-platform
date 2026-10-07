package exports

import "strings"

// Entry 是导出映射表中的一个有序键值对。
type Entry struct {
	Key    string
	Target Target
}

// wildcardEntry 是含星号键的索引项。
type wildcardEntry struct {
	key    string
	target Target
}

// ExportTable 是构造完成后不可修改的导出映射表。
// 键选择所需的数据结构在构造时一次性建好。
type ExportTable struct {
	// exact 存放不含星号的键，O(1) 精确查找。
	exact map[string]Target
	// wildcards 按 星号前缀 -> 星号后缀 -> 索引项 组织，
	// 使通配匹配只依赖请求长度，与表中键总数无关。
	wildcards map[string]map[string]wildcardEntry
	// probes 统计一次键选择中的 map 查找次数，用于复杂度验证。
	probes probeCounter
}

// NewTable 校验 entries 并构造不可变的导出映射表。
// 任何一条校验失败都返回 KindInvalidTable 错误。
func NewTable(entries []Entry) (*ExportTable, error) {
	t := &ExportTable{
		exact:     make(map[string]Target),
		wildcards: make(map[string]map[string]wildcardEntry),
	}
	for _, e := range entries {
		if err := validateKey(e.Key); err != nil {
			return nil, err
		}
		if err := e.Target.validate(e.Key); err != nil {
			return nil, err
		}
		if strings.IndexByte(e.Key, '*') < 0 {
			if _, dup := t.exact[e.Key]; dup {
				return nil, errf(KindInvalidTable, "重复的键 %q", e.Key)
			}
			t.exact[e.Key] = e.Target
			continue
		}
		star := strings.IndexByte(e.Key, '*')
		prefix, suffix := e.Key[:star], e.Key[star+1:]
		group, ok := t.wildcards[prefix]
		if !ok {
			group = make(map[string]wildcardEntry)
			t.wildcards[prefix] = group
		}
		if _, dup := group[suffix]; dup {
			return nil, errf(KindInvalidTable, "重复的键 %q", e.Key)
		}
		group[suffix] = wildcardEntry{key: e.Key, target: e.Target}
	}
	return t, nil
}

// validateKey 校验键的形态：恰为一个点，或以点斜杠开头，且至多一个星号。
func validateKey(key string) error {
	if key != "." && !strings.HasPrefix(key, "./") {
		return errf(KindInvalidTable, "键 %q 必须恰为 \".\" 或以 \"./\" 开头", key)
	}
	if strings.Count(key, "*") > 1 {
		return errf(KindInvalidTable, "键 %q 含多于一个星号", key)
	}
	return nil
}

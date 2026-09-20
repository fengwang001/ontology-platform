package ontology

import (
	"fmt"
	"reflect"
	"sort"
)

// SelfCheck 校验索引与存活记录一致：
// 任一约束下同一规范化键不得对应多条存活记录，且索引与记录完全同步。
// 返回 nil 表示一致；测试可直接调用。
func (c *Checker) SelfCheck() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	pks := make([]string, 0, len(c.records))
	for pk := range c.records {
		pks = append(pks, pk)
	}
	sort.Strings(pks)

	rebuilt := make(map[string]map[string]string, len(c.constraints))
	for _, con := range c.constraints {
		m := map[string]string{}
		for _, pk := range pks {
			key, ok := c.keyOf(con, c.records[pk])
			if !ok {
				continue
			}
			if other, dup := m[key]; dup {
				return fmt.Errorf("selfcheck: constraint %q: normalized key maps to multiple live records %q and %q",
					con.Name, other, pk)
			}
			m[key] = pk
		}
		rebuilt[con.Name] = m
	}
	if !reflect.DeepEqual(rebuilt, c.index) {
		return fmt.Errorf("selfcheck: index out of sync with live records")
	}
	return nil
}

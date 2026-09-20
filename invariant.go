package ontology

import "fmt"

// CheckInvariants 自检索引一致性，可被测试直接调用：
//  1. 任一约束下，同一规范化键至多对应一条存活记录；
//  2. 索引与记录集完全一致（不多不少、指向正确）。
//
// 发现问题时返回描述性错误，否则返回 nil。
func (c *Checker) CheckInvariants() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	seen := make(map[string]map[string]string, len(c.cons))
	for pk, props := range c.records {
		for _, con := range c.cons {
			key, _, _, ok := con.normKey(props, c.opts)
			if !ok {
				continue
			}
			if seen[con.Name] == nil {
				seen[con.Name] = make(map[string]string)
			}
			if prev, dup := seen[con.Name][key]; dup {
				return fmt.Errorf("constraint %q: normalized key %s held by both %q and %q",
					con.Name, displayKeyFromRaw(key), prev, pk)
			}
			seen[con.Name][key] = pk

			got, ok := c.index[con.Name][key]
			if !ok {
				return fmt.Errorf("constraint %q: missing index entry for record %q", con.Name, pk)
			}
			if got != pk {
				return fmt.Errorf("constraint %q: index points to %q, want %q", con.Name, got, pk)
			}
		}
	}

	total := 0
	for name, m := range c.index {
		for key, pk := range m {
			total++
			if _, alive := c.records[pk]; !alive {
				return fmt.Errorf("constraint %q: stale index entry for deleted record %q", name, pk)
			}
			if seen[name][key] != pk {
				return fmt.Errorf("constraint %q: index entry not backed by any live record", name)
			}
		}
	}
	want := 0
	for _, m := range seen {
		want += len(m)
	}
	if total != want {
		return fmt.Errorf("index has %d entries, want %d", total, want)
	}
	return nil
}

// displayKeyFromRaw 仅用于自检报错信息，直接展示编码键。
func displayKeyFromRaw(key string) string { return fmt.Sprintf("%q", key) }

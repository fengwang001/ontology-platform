package ontology

import (
	"fmt"
	"sort"
)

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

// cloneProps 深拷贝属性集合，隔离调用方对内部状态的后续修改。
func cloneProps(p Props) Props {
	if p == nil {
		return nil
	}
	out := make(Props, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// sortedIDs 返回按字典序排列的实例 id，作为存量实例回填的稳定内部顺序。
func sortedIDs(m map[string]*instance) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

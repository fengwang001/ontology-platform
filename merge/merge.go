// Package merge 按优先级把多层条目合并成最终键值表，并保留各层历史。
package merge

import "ontology/source"

// Record 是一个最终键的合并结果：胜出层、胜出原始值与各层历史。
type Record struct {
	Key     string
	Value   string
	Layer   source.Layer
	History []source.Entry // 按优先级从低到高
}

// Merger 执行合并并统计 map 查找次数（只计读，不计首次写入）。
type Merger struct {
	lookups int
}

// Merge 单遍扫描：layers 按优先级从低到高给出，每条目恰好一次查找；
// 同键高层覆盖低层，同层内后者覆盖前者。结果与层的构造顺序无关。
func (m *Merger) Merge(layers ...[]source.Entry) map[string]*Record {
	records := map[string]*Record{}
	for _, entries := range layers {
		for _, e := range entries {
			m.lookups++
			rec := records[e.Key]
			if rec == nil {
				rec = &Record{Key: e.Key}
				records[e.Key] = rec
			}
			rec.Value = e.Value
			rec.Layer = e.Layer
			rec.History = append(rec.History, e)
		}
	}
	return records
}

// Values 返回合并后的最终键值表（未展开）。
func Values(records map[string]*Record) map[string]string {
	values := make(map[string]string, len(records))
	for key, rec := range records {
		values[key] = rec.Value
	}
	return values
}

// Layers 返回每个最终键的胜出层。
func Layers(records map[string]*Record) map[string]source.Layer {
	layers := make(map[string]source.Layer, len(records))
	for key, rec := range records {
		layers[key] = rec.Layer
	}
	return layers
}

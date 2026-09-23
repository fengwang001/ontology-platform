// Package trace 为每个最终键生成来源溯源：胜出层、各层原始值与展开值。
package trace

import (
	"fmt"
	"sort"
	"strings"

	"ontology/merge"
	"ontology/source"
)

// LayerValue 记录某一层为一个键提供过的原始值。
type LayerValue struct {
	Layer source.Layer
	Value string
}

// KeyTrace 是一个键的完整溯源。
type KeyTrace struct {
	Key      string
	Source   source.Layer // 最终值来源层
	Raw      string       // 胜出层的原始值（展开前）
	Expanded string       // 展开后的最终值
	History  []LayerValue // 被覆盖层在前，按优先级从低到高
}

// Report 是按键字典序排列的溯源报告，输出确定。
type Report struct {
	Traces []KeyTrace
}

// Build 在合并记录（展开前的原始值）之上补入展开后的最终值。
func Build(records map[string]*merge.Record, expanded map[string]string) *Report {
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	report := &Report{Traces: make([]KeyTrace, 0, len(keys))}
	for _, key := range keys {
		rec := records[key]
		kt := KeyTrace{
			Key:      key,
			Source:   rec.Layer,
			Raw:      rec.Value,
			Expanded: expanded[key],
		}
		for _, e := range rec.History {
			kt.History = append(kt.History, LayerValue{Layer: e.Layer, Value: e.Value})
		}
		report.Traces = append(report.Traces, kt)
	}
	return report
}

// Find 返回指定键的溯源。
func (r *Report) Find(key string) (KeyTrace, bool) {
	for _, kt := range r.Traces {
		if kt.Key == key {
			return kt, true
		}
	}
	return KeyTrace{}, false
}

// String 输出确定的逐字节报告：键字典序、层序固定。
func (r *Report) String() string {
	var b strings.Builder
	for _, kt := range r.Traces {
		fmt.Fprintf(&b, "%s: source=%s raw=%q expanded=%q history=[", kt.Key, kt.Source, kt.Raw, kt.Expanded)
		for i, h := range kt.History {
			if i > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "%s:%q", h.Layer, h.Value)
		}
		b.WriteString("]\n")
	}
	return b.String()
}

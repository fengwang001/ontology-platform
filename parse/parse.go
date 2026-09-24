// Package parse 把原始字节行解析成分组记录；坏记录只计数不中断管线。
package parse

import (
	"strconv"

	"ontology/source"
)

// Record 是一条解析成功的记录。
type Record struct {
	Key   string
	Value int64
}

// Envelope 是 parse 阶段向下游发送的事件。
type Envelope struct {
	Offset  int64
	Barrier int64
	Bad     bool
	Rec     Record
}

// ParseLine 解析一行 "key,value"。空 key 合法；缺字段、value 非数字、
// 空行均判为坏记录（ok=false）。
func ParseLine(data []byte) (Record, bool) {
	if len(data) == 0 {
		return Record{}, false
	}
	comma := -1
	for i, b := range data {
		if b == ',' {
			comma = i
			break
		}
	}
	if comma < 0 {
		return Record{}, false
	}
	if comma == len(data)-1 {
		return Record{}, false
	}
	v, err := strconv.ParseInt(string(data[comma+1:]), 10, 64)
	if err != nil {
		return Record{}, false
	}
	return Record{Key: string(data[:comma]), Value: v}, true
}

// Convert 把一个 source 元素转换为下游信封；屏障原样透传。
// 第二个返回值表示是否为好记录（屏障恒为 true）。
func Convert(it source.Item) (Envelope, bool) {
	if it.Barrier > 0 {
		return Envelope{Offset: it.Offset, Barrier: it.Barrier}, true
	}
	env := Envelope{Offset: it.Offset}
	rec, ok := ParseLine(it.Data)
	if !ok {
		env.Bad = true
		return env, false
	}
	env.Rec = rec
	return env, true
}

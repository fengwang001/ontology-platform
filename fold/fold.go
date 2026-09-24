// Package fold 实现变更流的压实本体。依赖 ev。
package fold

import "ontology/ev"

// Compact 把 stream 改写成更短的等价流（骨架：暂原样返回）。
func Compact(stream []ev.Event) ([]ev.Event, error) {
	for _, e := range stream {
		if err := e.Validate(); err != nil {
			return nil, err
		}
	}
	out := make([]ev.Event, len(stream))
	copy(out, stream)
	return out, nil
}

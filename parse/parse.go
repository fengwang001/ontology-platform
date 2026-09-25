// Package parse 把原始字节行解析为 stage.Item。
package parse

import (
	"errors"
	"strconv"

	"ontology/source"
)

// ErrBad 是坏记录错误（缺分隔符或值非数字）。
var ErrBad = errors.New("parse: bad record")

// Parse 解析一行：格式必须为 "key=number"。
// key 允许为空串（行以 '=' 开头）；缺 '=' 或值非整数即坏记录。
func Parse(raw []byte) (key string, val int64, err error) {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '=' {
			continue
		}
		v, perr := strconv.ParseInt(string(raw[i+1:]), 10, 64)
		if perr != nil {
			return "", 0, ErrBad
		}
		return string(raw[:i]), v, nil
	}
	return "", 0, ErrBad
}

// Run 从 in 读原始记录，解析后发送到 out；坏记录经 bad 计数，屏障原样透传。
func Run(in <-chan source.Record, out chan<- source.Item, bad func()) {
	for r := range in {
		if r.IsBarrier() {
			out <- source.Barrier(r.Off)
			continue
		}
		key, val, err := Parse(r.Raw)
		if err != nil {
			if bad != nil {
				bad()
			}
			continue
		}
		out <- source.Item{Off: r.Off, Key: key, Val: val}
	}
}

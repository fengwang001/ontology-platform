package stm

import (
	"bytes"
	"runtime"
	"strconv"
)

// goid 返回当前 goroutine 的 id，用于检测事务体内再启动事务。
func goid() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// 格式："goroutine 123 [..."
	frame := buf[:n]
	const prefix = len("goroutine ")
	rest := frame[prefix:]
	end := bytes.IndexByte(rest, ' ')
	if end < 0 {
		return -1
	}
	id, err := strconv.ParseInt(string(rest[:end]), 10, 64)
	if err != nil {
		return -1
	}
	return id
}

// Package stream 定义已排序输入流的抽象。
package stream

import (
	"errors"

	"ontology/row"
)

// ErrStream 包装输入流自身的读取故障，用 errors.Is 判定。
var ErrStream = errors.New("stream: read failure")

// Stream 是一路按键升序的输入流。
// Next 返回下一行；流结束时 ok=false、err=nil；中途故障时 err 非空。
type Stream interface {
	Next() (r row.Row, ok bool, err error)
	Close() error
	// ConsumedKey 返回最近一次成功读出的键；故障后用于上报已消费位置。
	ConsumedKey() string
	// Index 返回已成功读出的行数（从零计的下一位置）。
	Index() int
}

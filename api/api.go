// Package api 对外提供不区分大小写的查找表，状态存于进程内存。
package api

import (
	"sync/atomic"

	"ontology/keymap"
)

// table 为进程内唯一的表；用原子指针保证并发只读安全。
var table atomic.Pointer[keymap.Map]

func init() {
	Init(256, 1<<20)
}

// Init 以给定上限重置表（键长按 rune 计）；构建期调用，勿与并发读写同时进行。
func Init(maxKeyLen, maxEntries int) {
	table.Store(keymap.New(maxKeyLen, maxEntries))
}

// Put 写入记录；空键、超长键、表满分别返回不同的哨兵错误。
func Put(key, value string) error {
	return table.Load().Put(key, value)
}

// Get 按键查找；Fold 相等的键命中同一条记录。
func Get(key string) (string, bool) {
	e, ok := table.Load().Get(key)
	return e.Value, ok
}

// Keys 按折叠键的字典序返回首次插入的原始键。
func Keys() []string {
	return table.Load().Keys()
}

// SelfCheck 核验表项与折叠键的一致性，可供测试直接调用。
func SelfCheck() error {
	return table.Load().SelfCheck()
}

// Package antientropy 实现基于版本向量（version vector）的增量反熵同步。
//
// 每条变更由 (Source, Seq) 全局唯一标识；副本本地写入时递增自身来源序号
// 并推进版本向量。同步时目标发送自己的版本向量，来源仅回送序号超过目标
// 已见值的最小差集（按来源名、序号有序），目标校验序号连续后原子应用。
// 读视图对每个键取 (时间戳, 来源, 序号) 字典序最大者的值。
//
// 非法输入（未注册副本、序号不连续、向量含负数、批量超限）会被整体拒绝，
// 拒绝不改变双方任何状态。所有操作可被多执行体并发调用。
package antientropy

import "sync"

// Version 为版本向量：来源副本名 -> 该来源已见的最大连续序号。
type Version map[string]int

// Change 是一条全局唯一标识的写入变更。
// 标识 = (Source, Seq)：某来源副本的第 Seq 次本地写入。
type Change struct {
	Source string
	Seq    int
	Key    string
	Value  string
	// TS 为本地写入时由副本分配的单调逻辑时间戳，用于读视图裁决。
	TS int
}

// Registry 管理所有已注册副本。
type Registry struct {
	mu       sync.RWMutex
	replicas map[string]*Replica
}

// Config 为副本配置。
type Config struct {
	// MaxBatch 为单次同步允许携带的变更条数上限；0 表示不限制。
	MaxBatch int
}

// Replica 是一个持有本地写入日志、版本向量与读视图的副本。
type Replica struct {
	name     string
	reg      *Registry
	mu       sync.Mutex
	clock    int                 // 本地逻辑时钟（写入时间戳）
	vector   Version             // 版本向量：来源 -> 已连续应用的最大序号
	log      []Change            // 按 (Source, Seq) 有序的已应用变更日志
	idx      map[string][]Change // 来源 -> 该来源序号连续的变更
	maxBatch int
}

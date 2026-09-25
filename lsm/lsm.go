// Package lsm 管理不可变 SSTable 列表（落盘序）：从新到旧读取，
// Compact 按全局 seq 取每 key 胜者并按规则保留墓碑。
// 依赖方向：lsm → mem，不允许反向依赖。
package lsm

import "ontology/mem"

// probeBound 是单表内定位一个 key 允许检查的条目常数上界（map 定位）。
const probeBound = 2

type ssTable struct {
	data map[string]mem.Entry // 不可变；map 定位，O(1)
}

// LSM 不可并发使用，由持有它的上层加锁。
type LSM struct {
	tables  []*ssTable // 落盘序：index 0 最旧，末尾最新
	probe   int        // 非导出：最近一次 Get 在单个 SSTable 内检查过的条目数
	readAmp int        // 最近一次 Get 实际检查过的 SSTable 个数
}

func New() *LSM { return &LSM{} }

// Freeze 把 memtable Drain 出的有序条目冻结成一个新 SSTable，追加为最新。
func (l *LSM) Freeze(kvs []mem.KV) {
	t := &ssTable{data: make(map[string]mem.Entry, len(kvs))}
	for _, kv := range kvs {
		t.data[kv.Key] = kv.Entry
	}
	l.tables = append(l.tables, t)
}

// TableCount 返回当前 SSTable 个数。
func (l *LSM) TableCount() int { return len(l.tables) }

// ReadAmp 返回最近一次 Get 检查过的 SSTable 个数。
func (l *LSM) ReadAmp() int { return l.readAmp }

// ProbeBounded 只返回布尔判定，不泄露 probe 数值：
// 最近一次单表定位检查数是否不随表规模增长（≤ 常数 probeBound）。
func (l *LSM) ProbeBounded() bool { return l.probe <= probeBound }

// Get 从最新 SSTable 向最旧查，第一个含 key 的表决定结果。
func (l *LSM) Get(key string) (mem.Entry, bool) {
	l.readAmp = 0
	for i := len(l.tables) - 1; i >= 0; i-- {
		l.probe = 0                             // 计数器只统计「单个 SSTable 内」的检查数
		if e, ok := l.tables[i].data[key]; ok { // map 定位：恒检查 1 个逻辑条目
			l.probe = 1
			l.readAmp++
			return e, true
		}
		l.readAmp++
	}
	return mem.Entry{}, false
}

// Compact 把所有 SSTable 合并成一个：每 key 取 seq 最大者为胜；
// 胜者是值则保留；胜者是墓碑时，仅当参与表中还存在更旧值才保留墓碑，
// 否则丢弃（墓碑是唯一条目）。
func (l *LSM) Compact() {
	if len(l.tables) == 0 {
		return
	}
	best := make(map[string]mem.Entry)
	hasValue := make(map[string]bool)
	for _, t := range l.tables { // 落盘序遍历，靠 seq 比较取胜，不依赖遍历顺序
		for k, e := range t.data {
			if w, ok := best[k]; !ok || e.Seq > w.Seq {
				best[k] = e
			}
			if !e.Deleted {
				hasValue[k] = true
			}
		}
	}
	out := make(map[string]mem.Entry, len(best))
	for k, w := range best {
		if w.Deleted && !hasValue[k] { // 唯一条目是墓碑：丢弃，key 删除
			continue
		}
		out[k] = w // 值，或仍需压住更旧值的墓碑
	}
	l.tables = []*ssTable{{data: out}}
}

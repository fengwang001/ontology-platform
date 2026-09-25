// Package rep 实现单个内存副本：applied 位点推进、key 值更新、按区间 catch-up。
package rep

import "ontology/log"

// Replica 是一个内存副本：截至 applied 的 key 值视图。
type Replica struct {
	applied int
	data    map[string]string
	online  bool
	checked int // 最近一次 catch-up 检查过的日志条目个数（非导出，不进公开接口）
}

// New 返回 applied=0 的 Online 副本。
func New() *Replica {
	return &Replica{data: make(map[string]string), online: true}
}

// Apply 应用一条写：推进 applied 并更新 key 值。调用方保证 e.LSN == applied+1。
func (r *Replica) Apply(e log.Entry) {
	r.data[e.Key] = e.Val
	r.applied = e.LSN
}

// CatchUp 把日志中 lsn ∈ (applied, target] 的写逐条应用，把副本补到 target。
// 只检查该区间内的条目，checked 记录本次检查个数。
func (r *Replica) CatchUp(l *log.Log, target int) {
	if target <= r.applied {
		r.checked = 0
		return
	}
	entries := l.Range(r.applied, target)
	r.checked = len(entries)
	for _, e := range entries {
		r.Apply(e)
	}
}

// Applied 返回已应用到的最新 lsn。
func (r *Replica) Applied() int { return r.applied }

// Get 返回 key 截至 applied 的值；不存在返回零值。
func (r *Replica) Get(key string) string { return r.data[key] }

// SetOnline 置上/下线：Offline 期间不接收新写。
func (r *Replica) SetOnline(on bool) { r.online = on }

// IsOnline 报告副本是否在线。
func (r *Replica) IsOnline() bool { return r.online }

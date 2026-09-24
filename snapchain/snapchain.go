// Package snapchain 快照链：快照号、提交时间戳、文件集、当前快照与保留判定。
// 不依赖其他包。
package snapchain

import "sort"

// Snapshot 一次提交的不可变记录；Files 升序且提交后不再被修改。
type Snapshot struct {
	ID    int      // 快照号，从 1 起递增（对外写作 S<ID>）
	Ts    int64    // 提交时间戳，严格递增
	Files []string // 该快照引用的数据文件
}

// Chain 按提交序保存现存快照，末尾为当前快照。
type Chain struct {
	snaps []Snapshot
	next  int // 下一个快照号；过期不回退
}

// Current 返回当前快照；空链时 ok=false。
func (c *Chain) Current() (s Snapshot, ok bool) {
	if len(c.snaps) == 0 {
		return Snapshot{}, false
	}
	return c.snaps[len(c.snaps)-1], true
}

// Len 返回现存快照个数。
func (c *Chain) Len() int { return len(c.snaps) }

// Snapshots 返回现存快照的副本（快照本身不可变，共享 Files 底层数组是安全的）。
func (c *Chain) Snapshots() []Snapshot {
	out := make([]Snapshot, len(c.snaps))
	copy(out, c.snaps)
	return out
}

// Commit 追加新快照并返回其快照号；参数合法性由调用方保证。
func (c *Chain) Commit(ts int64, files []string) int {
	c.next++
	c.snaps = append(c.snaps, Snapshot{ID: c.next, Ts: ts, Files: files})
	return c.next
}

// Expire 按三条规则（取并集）划分保留与过期：最新 n 个之一、ts>t、当前快照。
// 链中只保留「保留」快照，返回 (保留, 过期)。空链幂等。
func (c *Chain) Expire(n int, t int64) (kept, expired []Snapshot) {
	m := len(c.snaps)
	for i, s := range c.snaps {
		if i >= m-n || s.Ts > t || i == m-1 {
			kept = append(kept, s)
		} else {
			expired = append(expired, s)
		}
	}
	c.snaps = kept
	return kept, expired
}

// NaiveExpire 保留与可删除文件判定的朴素参照实现：按三条规则逐个判定保留，
// 再对全部过期快照与全部保留快照的文件做集合差。供测试与演示对照真实实现。
func NaiveExpire(snaps []Snapshot, n int, t int64) (kept []Snapshot, del []string) {
	gone := map[string]bool{}
	for i, s := range snaps {
		if i >= len(snaps)-n || s.Ts > t || i == len(snaps)-1 {
			kept = append(kept, s)
		} else {
			for _, f := range s.Files {
				gone[f] = true
			}
		}
	}
	for _, s := range kept {
		for _, f := range s.Files {
			delete(gone, f)
		}
	}
	del = []string{}
	for f := range gone {
		del = append(del, f)
	}
	sort.Strings(del)
	return kept, del
}

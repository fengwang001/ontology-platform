// Package api 是对外门面：并发安全的 Feed/View/Dropped 与内置自检。依赖 fww。
package api

import (
	"fmt"
	"sync"

	"ontology/fww"
)

// 类型与哨兵错误从 fww 再导出，调用方只需面对 api。
type (
	Write  = fww.Write
	Change = fww.Change
)

var (
	ErrEmptyKey = fww.ErrEmptyKey
	ErrBadSeq   = fww.ErrBadSeq
	ErrDupSeq   = fww.ErrDupSeq
)

// Register 是并发安全的 FWW 去重寄存器。
type Register struct {
	mu sync.RWMutex
	m  *fww.M
}

func New() *Register { return &Register{m: fww.New()} }

// Feed 应用一批写入；任一条被拒则整批不生效，返回可判定错误。
func (r *Register) Feed(ws []Write) ([]Change, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.m.Feed(ws)
}

// View 返回 Key→生效 Val 的物化视图（副本）。
func (r *Register) View() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.m.View()
}

// Dropped 返回累积丢弃数。
func (r *Register) Dropped() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.m.Dropped()
}

// SelfCheck 在内置写入序列上核验四条不变量，全部通过返回 nil。
// 只用局部实例，不触碰 r 的状态，可并发调用。
func (r *Register) SelfCheck() error {
	seqs := [][]Write{
		{{Key: "K", Seq: 7, Val: "a"}, {Key: "K", Seq: 3, Val: "b"}, {Key: "K", Seq: 10, Val: "c"},
			{Key: "K", Seq: 1, Val: "d"}, {Key: "K", Seq: 5, Val: "e"}, {Key: "K", Seq: 8, Val: "f"}},
		{{Key: "x", Seq: 2, Val: "p"}, {Key: "y", Seq: 9, Val: "q"}, {Key: "x", Seq: 1, Val: "r"},
			{Key: "y", Seq: 4, Val: "s"}, {Key: "x", Seq: 5, Val: "t"}},
	}
	for _, ws := range seqs {
		if err := checkInvariants(ws); err != nil {
			return err
		}
	}
	return checkFailureAtomic()
}

// checkInvariants 逐条喂入并核验不变量 1/2/3。
func checkInvariants(ws []Write) error {
	reg := New()
	shadow := map[string]Change{} // 用 changelog 重放的影子视图
	minSeq := map[string]int64{}  // 不变量 3：每键生效 Seq 只减不增
	want := map[string]string{}   // 不变量 1：批量重算（每键最小 Seq 的 Val）
	best := map[string]int64{}
	for _, w := range ws {
		if s, ok := best[w.Key]; !ok || w.Seq < s {
			best[w.Key], want[w.Key] = w.Seq, w.Val
		}
		log, err := reg.Feed([]Write{w})
		if err != nil {
			return fmt.Errorf("selfcheck: feed: %w", err)
		}
		for _, c := range log {
			if c.Retract { // 不变量 2：撤回必须恰好是当前值
				if cur, ok := shadow[c.Key]; !ok || cur.Seq != c.Seq || cur.Val != c.Val {
					return fmt.Errorf("selfcheck: retract mismatch at %q", c.Key)
				}
				delete(shadow, c.Key)
			} else {
				if _, dup := shadow[c.Key]; dup { // 不变量 2：同时至多一个生效值
					return fmt.Errorf("selfcheck: double add at %q", c.Key)
				}
				shadow[c.Key] = c
				if s, ok := minSeq[c.Key]; ok && c.Seq > s { // 不变量 3
					return fmt.Errorf("selfcheck: seq increased at %q", c.Key)
				}
				minSeq[c.Key] = c.Seq
			}
		}
	}
	view := reg.View() // 不变量 1：与批量重算逐 Key 相同
	if len(view) != len(want) {
		return fmt.Errorf("selfcheck: view size %d != %d", len(view), len(want))
	}
	for k, v := range want {
		if view[k] != v {
			return fmt.Errorf("selfcheck: view[%q]=%q want %q", k, view[k], v)
		}
	}
	return nil
}

// checkFailureAtomic 核验不变量 4：被拒整批不留痕，之后仍可用。
func checkFailureAtomic() error {
	reg := New()
	if _, err := reg.Feed([]Write{{Key: "K", Seq: 5, Val: "a"}}); err != nil {
		return fmt.Errorf("selfcheck: seed: %w", err)
	}
	before, beforeDrop := reg.View(), reg.Dropped()
	bads := [][]Write{
		{{Key: "", Seq: 1, Val: "x"}},
		{{Key: "K", Seq: 0, Val: "x"}},
		{{Key: "K", Seq: 5, Val: "x"}},
		{{Key: "ok", Seq: 1, Val: "y"}, {Key: "K", Seq: -1, Val: "x"}}, // 部分非法也要整批回滚
	}
	for i, b := range bads {
		if _, err := reg.Feed(b); err == nil {
			return fmt.Errorf("selfcheck: bad batch %d accepted", i)
		}
	}
	if reg.Dropped() != beforeDrop || len(reg.View()) != len(before) || reg.View()["K"] != before["K"] {
		return fmt.Errorf("selfcheck: rejected batch left trace")
	}
	if _, err := reg.Feed([]Write{{Key: "K", Seq: 1, Val: "z"}}); err != nil {
		return fmt.Errorf("selfcheck: unusable after rejection: %w", err)
	}
	return nil
}

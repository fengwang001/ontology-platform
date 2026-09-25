// Package api 对外门面：追加事件、快照、重放、自检。依赖 replay。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/es"
	"ontology/replay"
)

// Store 是进程内存中的聚合：下界为 0 的余额 + 事件日志。
type Store struct {
	mu      sync.RWMutex
	lastSeq int64
	balance int64
	evs     []es.Event
	rep     *replay.Replayer
}

// New 返回空聚合，balance=0。
func New() *Store { return &Store{rep: replay.New()} }

// Append 追加一条事件：Seq 必须大于已追加的最后一条。
// 先校验后变更，被拒时不改变任何状态。
func (s *Store) Append(ev es.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := es.ValidateAppend(s.lastSeq, ev); err != nil {
		return err
	}
	s.balance = es.Apply(s.balance, ev)
	s.lastSeq = ev.Seq
	s.evs = append(s.evs, ev)
	return nil
}

// State 返回当前状态 {最后一条事件序号, 当前余额}，可并发调用。
func (s *Store) State() es.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return es.Snapshot{Seq: s.lastSeq, Total: s.balance}
}

// Snapshot 拍下当前状态快照，可并发调用。
func (s *Store) Snapshot() es.Snapshot { return s.State() }

// Replay 从 snap 出发重放 evs，不改动 Store 自身状态。
func (s *Store) Replay(snap es.Snapshot, evs []es.Event) (int64, error) {
	return s.rep.Replay(snap, evs)
}

// SelfCheck 对内置事件序列核验四条不变量，全部通过返回 nil。可并发调用。
func (s *Store) SelfCheck() error {
	evs := []es.Event{{Seq: 1, Delta: 10}, {Seq: 2, Delta: -3}, {Seq: 3, Delta: 7}, {Seq: 4, Delta: -20}, {Seq: 5, Delta: 2}}
	rep := replay.New()
	full, err := rep.Replay(es.Snapshot{}, evs) // 零快照 = 从 0 全量重放
	if err != nil || full != 2 {
		return fmt.Errorf("selfcheck: full replay = %d, %v", full, err)
	}
	cut, err := rep.Replay(es.Snapshot{Seq: 3, Total: 14}, evs)
	if err != nil || cut != full { // 不变量 1：重放一致
		return fmt.Errorf("selfcheck: snapshot replay = %d != %d", cut, full)
	}
	again, _ := rep.Replay(es.Snapshot{Seq: 3, Total: 14}, evs)
	dup, _ := rep.Replay(es.Snapshot{Seq: 3, Total: 14},
		[]es.Event{{Seq: 1, Delta: 10}, {Seq: 2, Delta: -3}, {Seq: 3, Delta: 7}, {Seq: 4, Delta: -20}, {Seq: 4, Delta: -20}, {Seq: 5, Delta: 2}})
	if again != cut || dup != cut { // 不变量 2、3：幂等 + 快照边界
		return errors.New("selfcheck: not idempotent")
	}
	for _, bad := range []es.Snapshot{{Seq: -1}, {Total: -1}} { // 不变量 4：可判定错误
		if _, err := rep.Replay(bad, evs); !errors.Is(err, es.ErrBadSnapshot) {
			return errors.New("selfcheck: bad snapshot accepted")
		}
	}
	if _, err := rep.Replay(es.Snapshot{}, []es.Event{{Seq: 2, Delta: 1}, {Seq: 1, Delta: 1}}); !errors.Is(err, replay.ErrBadReplay) {
		return errors.New("selfcheck: unsorted replay accepted")
	}
	return nil
}

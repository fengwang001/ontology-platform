package scheduler

import (
	"sort"
	"sync"
)

// replayHook 在每个事务的回放 goroutine 内调用，仅用于测试观测并发执行。
var replayHook = func(*accepted) {}

// Replay 按 Schedule 得到的轮次并发回放所有已接受事务。
// 同一轮内的事务在独立 goroutine 中真正并发执行；
// 因同轮写集两两不相交，最终状态与按提交顺序串行回放逐键一致。
func (s *Scheduler) Replay(maxParallel int) (ReplayResult, error) {
	if maxParallel <= 0 {
		return ReplayResult{}, reject(ReasonInvalidParallelism, "max parallelism must be positive")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	txs := make([]*accepted, len(s.txs))
	copy(txs, s.txs)
	bySeq := make(map[int]*accepted, len(txs))
	for _, tx := range txs {
		bySeq[tx.seq] = tx
	}

	rounds := buildRounds(txs, maxParallel)
	state := map[string]string{}

	for _, round := range rounds {
		var wg sync.WaitGroup
		for _, seq := range round.Transaction {
			tx := bySeq[seq]
			wg.Add(1)
			go func(tx *accepted) {
				defer wg.Done()
				replayHook(tx)
				effects := make(map[string]string, len(tx.keys))
				for _, key := range tx.keys {
					effects[key] = tx.writeKV[key]
				}
				applyEffects(state, effects)
			}(tx)
		}
		wg.Wait()
	}

	depths := make(map[int]int, len(txs))
	for _, tx := range txs {
		depths[tx.seq] = tx.depth
	}

	return ReplayResult{
		State:       cloneState(state),
		Rounds:      rounds,
		Depth:       depths,
		SelfCheckOK: s.selfCheck(txs, rounds, state),
	}, nil
}

// applyEffects 合并一轮并发事务的互不相交效果，加锁保证 map 并发安全。
func applyEffects(state map[string]string, effects map[string]string) {
	stateMu.Lock()
	defer stateMu.Unlock()
	for key, value := range effects {
		state[key] = value
	}
}

var stateMu sync.Mutex

// selfCheck 用朴素串行参照逐事务复算最终状态，并校验轮次结构。
func (s *Scheduler) selfCheck(txs []*accepted, rounds []Round, got map[string]string) bool {
	reference := map[string]string{}
	for _, tx := range txs {
		for _, key := range tx.keys {
			reference[key] = tx.writeKV[key]
		}
	}
	if len(reference) != len(got) {
		return false
	}
	for key, value := range reference {
		if got[key] != value {
			return false
		}
	}

	seen := map[int]bool{}
	done := map[int]bool{}
	for _, round := range rounds {
		if len(round.Transaction) == 0 {
			return false
		}
		roundKeys := map[string]bool{}
		for _, seq := range round.Transaction {
			tx, ok := txsBySeq(txs, seq)
			if !ok || seen[seq] {
				return false
			}
			seen[seq] = true
			for _, key := range tx.keys {
				if roundKeys[key] {
					return false
				}
				roundKeys[key] = true
			}
			for _, depSeq := range tx.basis {
				if !done[depSeq] {
					return false
				}
			}
		}
		for _, seq := range round.Transaction {
			done[seq] = true
		}
	}

	seqs := make([]int, 0, len(seen))
	for seq := range seen {
		seqs = append(seqs, seq)
	}
	sort.Ints(seqs)
	for i, seq := range seqs {
		if seq != i+1 {
			return false
		}
	}

	s.logger.Info("replay self-check", "ok", true, "transactions", len(txs), "rounds", len(rounds))
	return true
}

func txsBySeq(txs []*accepted, seq int) (*accepted, bool) {
	idx := seq - 1
	if idx < 0 || idx >= len(txs) || txs[idx].seq != seq {
		return nil, false
	}
	return txs[idx], true
}

func cloneState(state map[string]string) map[string]string {
	clone := make(map[string]string, len(state))
	for key, value := range state {
		clone[key] = value
	}
	return clone
}

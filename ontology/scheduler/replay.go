package scheduler

import (
	"context"
	"fmt"
	"sync"
)

// defaultExecutor 是 Replay 在 exec 为 nil 时使用的回放执行体：
// 显式提供 WriteValues 的键写入其值，其余键写入确定性派生值，
// 不读取任何既有状态，因此结果完全可复现。
func defaultExecutor(tx Transaction) (map[string]string, error) {
	out := make(map[string]string, len(tx.WriteKeys))
	for _, k := range tx.WriteKeys {
		if v, ok := tx.WriteValues[k]; ok {
			out[k] = v
		} else {
			out[k] = fmt.Sprintf("tx%d:%s", tx.Seq, k)
		}
	}
	return out, nil
}

// Replay 在给定并行度上限下回放全部已接受事务并返回最终状态。
//
// 调度轮次与 Schedule 的结果一致；同一轮内的事务在不同 goroutine 上
// 真正并发执行，轮次之间存在屏障：一轮全部结束后下一轮才开始。
// 最终状态按轮次、轮内按序号顺序合并，因同轮写集互不相交，合并结果
// 与按提交顺序串行回放逐键一致。exec 为 nil 时使用确定性默认执行体。
func (s *Scheduler) Replay(ctx context.Context, maxParallel int, exec Executor) (*ReplayResult, error) {
	if maxParallel <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidParallel, maxParallel)
	}
	if exec == nil {
		exec = defaultExecutor
	}
	txns := s.snapshot()
	roundOf := computeRounds(txns, maxParallel)

	bySeq := make(map[int]*txn, len(txns))
	for _, t := range txns {
		bySeq[t.seq] = t
	}
	maxRound := 0
	for _, r := range roundOf {
		if r > maxRound {
			maxRound = r
		}
	}

	state := make(map[string]string)
	result := &ReplayResult{State: state}
	execOf := make(map[int]*TxExecution, len(txns))

	for r := 1; r <= maxRound; r++ {
		var seqs []int
		for _, t := range txns {
			if roundOf[t.seq] == r {
				seqs = append(seqs, t.seq)
			}
		}
		result.Rounds = append(result.Rounds, RoundPlan{
			Round:    r,
			TxSeqs:   append([]int(nil), seqs...),
			Parallel: len(seqs) > 1,
		})

		// 同轮事务真正并发执行；ctx 取消或任一执行失败即停止整轮回放。
		ctx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var firstErr error
		outs := make([]map[string]string, len(seqs))
		execs := make([]*TxExecution, len(seqs))
		for i, seq := range seqs {
			wg.Add(1)
			go func(i, seq int) {
				defer wg.Done()
				t := bySeq[seq]
				input := Transaction{
					Seq:         t.seq,
					ReadKeys:    append([]string(nil), t.readKeys...),
					WriteKeys:   append([]string(nil), t.writeKeys...),
					WriteValues: cloneValues(t.values),
				}
				record := &TxExecution{Seq: seq, Round: r}
				if err := ctx.Err(); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					cancel()
					return
				}
				record.Started = now()
				writes, err := exec(input)
				record.Ended = now()
				mu.Lock()
				execs[i] = record
				if err != nil {
					if firstErr == nil {
						firstErr = fmt.Errorf("tx %d executor failed: %w", seq, err)
					}
					cancel()
				} else {
					outs[i] = writes
				}
				mu.Unlock()
			}(i, seq)
		}
		wg.Wait()
		cancel()
		if firstErr != nil {
			return nil, firstErr
		}

		// 确定性合并：按序号顺序提交本轮结果，并校验执行体只写了声明的写键。
		for i, seq := range seqs {
			t := bySeq[seq]
			record := execs[i]
			writes := outs[i]
			if err := validateWrites(t, writes); err != nil {
				return nil, err
			}
			record.Writes = make(map[string]string, len(t.writeKeys))
			for _, k := range t.writeKeys {
				state[k] = writes[k]
				record.Writes[k] = writes[k]
			}
			execOf[seq] = record
		}
	}

	for _, t := range txns {
		if e := execOf[t.seq]; e != nil {
			result.Executions = append(result.Executions, *e)
		}
	}
	return result, nil
}

func cloneValues(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func validateWrites(t *txn, writes map[string]string) error {
	if len(writes) != len(t.writeKeys) {
		return fmt.Errorf("%w: tx %d executor returned %d writes, write set has %d keys",
			ErrInvalidArgument, t.seq, len(writes), len(t.writeKeys))
	}
	allowed := make(map[string]struct{}, len(t.writeKeys))
	for _, k := range t.writeKeys {
		allowed[k] = struct{}{}
	}
	for k := range writes {
		if _, ok := allowed[k]; !ok {
			return fmt.Errorf("%w: tx %d executor wrote undeclared key %q",
				ErrInvalidArgument, t.seq, k)
		}
	}
	return nil
}

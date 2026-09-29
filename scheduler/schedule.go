package scheduler

// Depths 返回每个事务序号到依赖深度的映射副本。
func (s *Scheduler) Depths() map[int]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	depths := make(map[int]int, len(s.txs))
	for _, tx := range s.txs {
		depths[tx.seq] = tx.depth
	}
	return depths
}

// Count 返回已接受事务总数。
func (s *Scheduler) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.txs)
}

// Schedule 按提交顺序逐轮调度：每轮最多 maxParallel 个事务，
// 且同轮事务的写集两两不相交（不互为依赖）。
func (s *Scheduler) Schedule(maxParallel int) ([]Round, error) {
	if maxParallel <= 0 {
		return nil, reject(ReasonInvalidParallelism, "max parallelism must be positive")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rounds := buildRounds(s.txs, maxParallel)
	for _, round := range rounds {
		for _, seq := range round.Transaction {
			s.logger.Info("scheduled transaction",
				"seq", seq,
				"round", round.Index,
				"max_parallel", maxParallel,
				"round_size", len(round.Transaction),
			)
		}
	}
	return rounds, nil
}

func buildRounds(txs []*accepted, maxParallel int) []Round {
	done := make(map[int]bool, len(txs))
	remaining := make([]*accepted, len(txs))
	copy(remaining, txs)

	var rounds []Round
	roundIndex := 0
	for len(remaining) > 0 {
		roundIndex++
		round := Round{Index: roundIndex}
		takenKeys := map[string]bool{}
		next := remaining[:0]
		for idx, tx := range remaining {
			if len(round.Transaction) >= maxParallel {
				for _, tail := range remaining[idx:] {
					next = append(next, tail)
				}
				break
			}
			ready := true
			for _, depSeq := range tx.basis {
				if !done[depSeq] {
					ready = false
					break
				}
			}
			if ready {
				conflicts := false
				for _, key := range tx.keys {
					if takenKeys[key] {
						conflicts = true
						break
					}
				}
				if conflicts {
					next = append(next, tx)
					continue
				}
				round.Transaction = append(round.Transaction, tx.seq)
				for _, key := range tx.keys {
					takenKeys[key] = true
				}
			} else {
				next = append(next, tx)
			}
		}
		remaining = next
		for _, seq := range round.Transaction {
			done[seq] = true
		}
		rounds = append(rounds, round)
	}
	return rounds
}

package scheduler

import "fmt"

// validateAndParse 在不触碰调度器状态的前提下校验并解析整批事务。
// 任何一个事务非法即返回对应原因的拒绝错误；调用方因此可以做到
// “整体拒绝、失败不留痕迹”。
//
// 校验顺序（同时也是错误优先级）：
//  1. 参数级：批次非空
//  2. 逐事务按序号顺序：序号必须为 len(txns)+i+1（连续、从 1 递增）
//  3. 逐事务：写集非空
//  4. 逐事务：写集不含空键
//  5. 容量：接收后总数不得超过上限
func (s *Scheduler) validateAndParse(batch []TxnInput) ([]*TxnInfo, error) {
	if len(batch) == 0 {
		return nil, reject(CodeInvalidArgument, "事务批次为空", "Accept 至少需要一个事务")
	}

	parsed := make([]*TxnInfo, 0, len(batch))
	for i, in := range batch {
		wantSeq := len(s.txns) + i + 1
		if in.Seq != wantSeq {
			return nil, reject(
				CodeSequenceGap,
				fmt.Sprintf("第 %d 个到达事务的序号不连续", i+1),
				fmt.Sprintf("期望 seq=%d, 实际 seq=%d", wantSeq, in.Seq),
			)
		}

		if len(in.WriteKeys) == 0 {
			return nil, reject(
				CodeEmptyWriteSet,
				fmt.Sprintf("事务 %d 写集为空", in.Seq),
				"每个事务至少写一个键",
			)
		}

		writeSet := make(map[string]struct{}, len(in.WriteKeys))
	orderedWrites := make([]string, 0, len(in.WriteKeys))
		for _, k := range in.WriteKeys {
			if k == "" {
				return nil, reject(
					CodeEmptyWriteKey,
					fmt.Sprintf("事务 %d 写集包含空键", in.Seq),
					"写键必须是非空字符串",
				)
			}
			if _, ok := writeSet[k]; !ok {
				writeSet[k] = struct{}{}
				orderedWrites = append(orderedWrites, k)
			}
		}

		reads := make([]string, len(in.ReadKeys))
		copy(reads, in.ReadKeys)

		parsed = append(parsed, &TxnInfo{
			Seq:       in.Seq,
			ReadKeys:  reads,
			WriteKeys: orderedWrites,
		})
	}

	if len(s.txns)+len(parsed) > s.maxTxns {
		return nil, reject(
			CodeTooManyTransactions,
			fmt.Sprintf("接收后事务总数 %d 超过上限 %d", len(s.txns)+len(parsed), s.maxTxns),
			fmt.Sprintf("已接受=%d, 本批=%d", len(s.txns), len(parsed)),
		)
	}

	return parsed, nil
}

// computeDepsAndDepth 计算一个新事务相对所有已接受事务的写集依赖与深度。
// 依赖定义：前序事务的写集与当前事务写集存在非空交集。
// 深度定义：无依赖为 1；否则为 1 + max(所有依赖事务的深度)。
func computeDepsAndDepth(t *TxnInfo, accepted []*TxnInfo, writeSets []map[string]struct{}) {
	target := writeSetOf(t)
	maxDepDepth := 0
	for i, prev := range accepted {
		if writeSetsIntersect(writeSets[i], target) {
			t.Deps = append(t.Deps, prev.Seq)
			if prev.Depth > maxDepDepth {
				maxDepDepth = prev.Depth
			}
		}
	}
	t.Depth = maxDepDepth + 1
	if len(t.Deps) == 0 {
		t.Basis = fmt.Sprintf("无写集相交的前序事务，深度=1")
	} else {
		t.Basis = fmt.Sprintf("写集相交前序事务=%v，其中最大深度=%d，深度=%d",
			t.Deps, maxDepDepth, t.Depth)
	}
}

func writeSetOf(t *TxnInfo) map[string]struct{} {
	m := make(map[string]struct{}, len(t.WriteKeys))
	for _, k := range t.WriteKeys {
		m[k] = struct{}{}
	}
	return m
}

func writeSetsIntersect(a, b map[string]struct{}) bool {
	// 让较小的集合驱动查找。
	if len(a) > len(b) {
		a, b = b, a
	}
	for k := range a {
		if _, ok := b[k]; ok {
			return true
		}
	}
	return false
}

// Accept 原子地接收一批按提交顺序连续到达的事务。
// 批次内任意事务非法则整体拒绝，调度器已有状态（事务、深度、
// 调度结果与回放状态均为按需派生）保持完全不变。
func (s *Scheduler) Accept(batch []TxnInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	parsed, err := s.validateAndParse(batch)
	if err != nil {
		return err
	}

	writeSets := make([]map[string]struct{}, len(s.txns))
	for i, t := range s.txns {
		writeSets[i] = writeSetOf(t)
	}

	for _, t := range parsed {
		computeDepsAndDepth(t, s.txns, writeSets)
		s.txns = append(s.txns, t)
		writeSets = append(writeSets, writeSetOf(t))
		s.logger.Info("scheduler: 事务已接受",
			"seq", t.Seq,
			"read_keys", t.ReadKeys,
			"write_keys", t.WriteKeys,
			"depth", t.Depth,
			"deps", t.Deps,
			"basis", t.Basis,
		)
	}
	return nil
}

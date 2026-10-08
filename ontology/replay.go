package ontology

import "fmt"

// ReplayDecisions 独立重放一份裁定决策日志，验证其内部一致性。
// 它不依赖产生日志的实现代码，而是依据公开语义重新推导：
// 每条决策的准入判定比较次数恒为 1（O(1) 准入证据）；
// 每个竞争者的分数可由（到达时刻, 失败次数, 逻辑时钟）重算；
// 最高优先级者必须按全序（分数、到达时刻、请求 ID）重新选出；
// 只有提交类决策才推进版本号，失败路径版本号不变；
// 冲突与被挤出恰好使失败次数加一，耗尽判定只发生在裁定落败之后；
// 逻辑时钟只在登记到达与提交时推进。
// 日志合法时返回 nil，否则返回描述第一条不一致记录的错误。
func ReplayDecisions(log []Decision) error {
	var version, seq uint64
	waiters := make(map[RequestID]Ticket)

	// headOf 按全序从当前重放等待集中选出最高优先级者。
	headOf := func() (RequestID, int) {
		var head RequestID
		var headKey priorityKey
		comparisons := 0
		first := true
		for id, t := range waiters {
			k := keyOf(id, t, seq)
			if first || headKey.less(k) {
				head, headKey = id, k
			}
			if !first {
				comparisons++
			}
			first = false
		}
		return head, comparisons
	}

	// checkContenders 校验竞争者快照与重放等待集一致（含分数与排序）。
	checkContenders := func(i int, d Decision) error {
		if len(d.Contenders) != len(waiters) {
			return fmt.Errorf("决策 #%d: 竞争者为 %d 个，重放等待集中为 %d 个", i, len(d.Contenders), len(waiters))
		}
		var prevKey priorityKey
		for j, c := range d.Contenders {
			t, ok := waiters[c.ID]
			if !ok {
				return fmt.Errorf("决策 #%d: 竞争者 %q 不在等待集中", i, c.ID)
			}
			if t.ArrivalSeq != c.ArrivalSeq || t.Failures != c.Failures {
				return fmt.Errorf("决策 #%d: 竞争者 %q 快照为 %+v，重放票据为 %+v", i, c.ID, c, t)
			}
			if want := t.Score(d.Seq); c.Score != want {
				return fmt.Errorf("决策 #%d: 竞争者 %q 分数为 %d，重算为 %d", i, c.ID, c.Score, want)
			}
			k := priorityKey{score: c.Score, arrival: c.ArrivalSeq, id: c.ID}
			if j > 0 && prevKey.less(k) {
				return fmt.Errorf("决策 #%d: 竞争者快照未按优先级降序排列", i)
			}
			prevKey = k
		}
		return nil
	}

	for i, d := range log {
		if d.AdmissionChecks != 1 {
			return fmt.Errorf("决策 #%d (%s): 准入判定比较次数为 %d，应为 1", i, d.Kind, d.AdmissionChecks)
		}

		switch d.Kind {
		case DecideFastCommit:
			if len(waiters) != 0 {
				return fmt.Errorf("决策 #%d: 快速路径提交时等待集非空（%d 个竞争者）", i, len(waiters))
			}
			if d.Seq != seq {
				return fmt.Errorf("决策 #%d: 裁定时刻为 %d，应为 %d", i, d.Seq, seq)
			}
			if d.Outcome != OutcomeCommitted {
				return fmt.Errorf("决策 #%d: 快速路径结果应为 committed，实际为 %s", i, d.Outcome)
			}
			version++
			seq++
			if d.Version != version {
				return fmt.Errorf("决策 #%d: 提交后版本号为 %d，应为 %d", i, d.Version, version)
			}

		case DecideConflict:
			if d.Outcome != OutcomeConflict {
				return fmt.Errorf("决策 #%d: 冲突决策结果应为 conflict，实际为 %s", i, d.Outcome)
			}
			if d.Version != version {
				return fmt.Errorf("决策 #%d: 冲突决策改变了版本号（%d -> %d）", i, version, d.Version)
			}
			if _, ok := waiters[d.Attempter]; !ok {
				// 新到达登记：到达时刻取当前逻辑时钟，随后时钟推进。
				if d.Ticket.ArrivalSeq != seq || d.Ticket.Failures != 1 {
					return fmt.Errorf("决策 #%d: 冲突登记票据应为 {ArrivalSeq: %d, Failures: 1}，实际为 %+v", i, seq, d.Ticket)
				}
				waiters[d.Attempter] = d.Ticket
				seq++
			} else {
				// 已登记等待者作为最高优先级者冲突：失败次数 +1。
				t := waiters[d.Attempter]
				t.Failures++
				if t != d.Ticket {
					return fmt.Errorf("决策 #%d: 冲突后票据为 %+v，重放推导为 %+v", i, d.Ticket, t)
				}
				waiters[d.Attempter] = t
			}
			if d.Seq != seq {
				return fmt.Errorf("决策 #%d: 裁定时刻为 %d，应为 %d", i, d.Seq, seq)
			}
			if err := checkContenders(i, d); err != nil {
				return err
			}
			head, comparisons := headOf()
			if head != d.Attempter {
				return fmt.Errorf("决策 #%d: 冲突者 %q 应为最高优先级者，重算为 %q", i, d.Attempter, head)
			}
			if d.Head != head || d.RoundComparisons != comparisons {
				return fmt.Errorf("决策 #%d: 裁定记录（head=%q, cmp=%d）与重算（%q, %d）不一致",
					i, d.Head, d.RoundComparisons, head, comparisons)
			}

		case DecidePreempted:
			if d.Outcome != OutcomePreempted {
				return fmt.Errorf("决策 #%d: 被挤出决策结果应为 preempted，实际为 %s", i, d.Outcome)
			}
			if d.Version != version {
				return fmt.Errorf("决策 #%d: 被挤出决策改变了版本号（%d -> %d）", i, version, d.Version)
			}
			if _, ok := waiters[d.Attempter]; !ok {
				// 新到达登记即落败：到达时刻取当前逻辑时钟，失败次数记 1。
				if d.Ticket.ArrivalSeq != seq || d.Ticket.Failures != 1 {
					return fmt.Errorf("决策 #%d: 新到达票据应为 {ArrivalSeq: %d, Failures: 1}，实际为 %+v", i, seq, d.Ticket)
				}
				waiters[d.Attempter] = d.Ticket
				seq++
			} else {
				t := waiters[d.Attempter]
				t.Failures++
				if t != d.Ticket {
					return fmt.Errorf("决策 #%d: 被挤出后票据为 %+v，重放推导为 %+v", i, d.Ticket, t)
				}
				waiters[d.Attempter] = t
			}
			if d.Seq != seq {
				return fmt.Errorf("决策 #%d: 裁定时刻为 %d，应为 %d", i, d.Seq, seq)
			}
			if err := checkContenders(i, d); err != nil {
				return err
			}
			head, comparisons := headOf()
			if head == d.Attempter {
				return fmt.Errorf("决策 #%d: 被挤出者 %q 不应为最高优先级者", i, d.Attempter)
			}
			if d.Head != head || d.RoundComparisons != comparisons {
				return fmt.Errorf("决策 #%d: 裁定记录（head=%q, cmp=%d）与重算（%q, %d）不一致",
					i, d.Head, d.RoundComparisons, head, comparisons)
			}

		case DecideCommit:
			if _, ok := waiters[d.Attempter]; !ok {
				return fmt.Errorf("决策 #%d: 提交者 %q 不在等待集中", i, d.Attempter)
			}
			if d.Outcome != OutcomeCommitted {
				return fmt.Errorf("决策 #%d: 提交决策结果应为 committed，实际为 %s", i, d.Outcome)
			}
			if d.Seq != seq {
				return fmt.Errorf("决策 #%d: 裁定时刻为 %d，应为 %d", i, d.Seq, seq)
			}
			if err := checkContenders(i, d); err != nil {
				return err
			}
			head, comparisons := headOf()
			if head != d.Attempter {
				return fmt.Errorf("决策 #%d: 提交者 %q 应为最高优先级者，重算为 %q", i, d.Attempter, head)
			}
			if d.Head != head || d.RoundComparisons != comparisons {
				return fmt.Errorf("决策 #%d: 裁定记录（head=%q, cmp=%d）与重算（%q, %d）不一致",
					i, d.Head, d.RoundComparisons, head, comparisons)
			}
			delete(waiters, d.Attempter)
			version++
			seq++
			if d.Version != version {
				return fmt.Errorf("决策 #%d: 提交后版本号为 %d，应为 %d", i, d.Version, version)
			}

		case DecideExhausted:
			t, ok := waiters[d.Attempter]
			if !ok {
				return fmt.Errorf("决策 #%d: 被耗尽请求 %q 不在等待集中", i, d.Attempter)
			}
			if d.Outcome != OutcomeExhausted {
				return fmt.Errorf("决策 #%d: 耗尽决策结果应为 exhausted，实际为 %s", i, d.Outcome)
			}
			if d.Version != version {
				return fmt.Errorf("决策 #%d: 耗尽决策改变了版本号（%d -> %d）", i, version, d.Version)
			}
			if d.Seq != seq {
				return fmt.Errorf("决策 #%d: 裁定时刻为 %d，应为 %d", i, d.Seq, seq)
			}
			if t != d.Ticket {
				return fmt.Errorf("决策 #%d: 耗尽时票据为 %+v，重放票据为 %+v", i, d.Ticket, t)
			}
			if t.Failures <= d.MaxRetries {
				return fmt.Errorf("决策 #%d: 失败次数 %d 未超过上限 %d，不应判定耗尽", i, t.Failures, d.MaxRetries)
			}
			head, _ := headOf()
			if head == d.Attempter {
				return fmt.Errorf("决策 #%d: 最高优先级者 %q 应获得提交权而非被判定耗尽", i, d.Attempter)
			}
			delete(waiters, d.Attempter)

		default:
			return fmt.Errorf("决策 #%d: 未知决策类型 %d", i, d.Kind)
		}
	}
	return nil
}

package ledger

// DrugSnapshot 是一种药品在某一时刻的账面快照。
type DrugSnapshot struct {
	Batches        map[string]int // 批号 -> 账面余量（含已过期、含 0）
	BookTotal      int            // 账面总量
	Available      int            // 可发库存（效期严格大于快照时刻）
	TotalIn        int
	TotalDestroyed int
	TotalIssued    int
	TotalReturned  int
}

// SlipSnapshot 是一张单据的快照。
type SlipSnapshot struct {
	Dept      string
	Applicant string
	Drug      string
	Lines     []BatchLine
	Qty       int
	IssueNow  int64
	Deadline  int64
	Status    SlipStatus
	Used      int
	Returned  int
	Residual  int
	Diff      int
}

// DeptSnapshot 是一个科室在某一时刻的快照。
type DeptSnapshot struct {
	Locked      bool
	OpenCount   int
	Discrepancy int
	TotalSlips  int
}

// Snapshot 是整个账册在 now 时刻的可比对快照。
type Snapshot struct {
	Now   int64
	Drugs map[string]DrugSnapshot
	Slips map[string]SlipSnapshot
	Depts map[string]DeptSnapshot
}

// Snapshot 导出账册在 now 时刻的完整可观测状态。
// now 应不小于当前时钟；该接口为只读诊断接口，不改变任何状态。
func (s *System) Snapshot(now int64) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{
		Now:   now,
		Drugs: make(map[string]DrugSnapshot, len(s.drugs)),
		Slips: make(map[string]SlipSnapshot, len(s.slips)),
		Depts: make(map[string]DeptSnapshot, len(s.depts)),
	}
	for name, d := range s.drugs {
		ds := DrugSnapshot{
			Batches:        make(map[string]int, len(d.batches)),
			BookTotal:      d.bookTotal,
			TotalIn:        d.totalIn,
			TotalDestroyed: d.totalDestroyed,
			TotalIssued:    d.totalIssued,
			TotalReturned:  d.totalReturned,
		}
		for id, b := range d.batches {
			ds.Batches[id] = b.qty
			if b.expiry > now {
				ds.Available += b.qty
			}
		}
		snap.Drugs[name] = ds
	}
	for id, sl := range s.slips {
		lines := make([]BatchLine, len(sl.lines))
		copy(lines, sl.lines)
		snap.Slips[id] = SlipSnapshot{
			Dept: sl.dept, Applicant: sl.applicant, Drug: sl.drug,
			Lines: lines, Qty: sl.qty, IssueNow: sl.issueNow, Deadline: sl.deadline,
			Status: sl.status, Used: sl.used, Returned: sl.returned,
			Residual: sl.residual, Diff: sl.diff,
		}
	}
	for name, dp := range s.depts {
		snap.Depts[name] = DeptSnapshot{
			Locked:      dp.locked(now),
			OpenCount:   dp.openCount(),
			Discrepancy: dp.discrepancy,
			TotalSlips:  dp.totalSlips,
		}
	}
	return snap
}

// InvariantOK 校验不变式：账面总量 == 入库总量 - 销毁总量 - 累计领出量 + 累计退回量。
func (s *System) InvariantOK() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.drugs {
		if d.bookTotal != d.totalIn-d.totalDestroyed-d.totalIssued+d.totalReturned {
			return false
		}
	}
	return true
}

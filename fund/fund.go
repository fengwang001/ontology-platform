package fund

import "sync"

// Acc 为某参保人某年度的五个累计量，单位分。
type Acc struct {
	DU int64 // 已用起付
	X  int64 // 已进入分段的累计额
	F  int64 // 基金已付
	P  int64 // 合规自付累计
	Q  int64 // 救助已付
}

// Fund 保存所有参保人的年度累计器。
type Fund struct {
	mu      sync.Mutex
	acc     map[string]map[int]*Acc
	maxYear map[string]int
}

// New 创建空账本。
func New() *Fund {
	return &Fund{acc: make(map[string]map[int]*Acc), maxYear: make(map[string]int)}
}

// Closed 报告该参保人是否已结算过比 year 更大的年度（年度已关闭）。
func (f *Fund) Closed(person string, year int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return year < f.maxYear[person]
}

// Before 返回结算前快照（累计量与当前最大年度），并登记新年度。
func (f *Fund) Before(person string, year int) (Acc, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	years, ok := f.acc[person]
	if !ok {
		years = make(map[int]*Acc)
		f.acc[person] = years
	}
	cur, ok := years[year]
	var snap Acc
	if ok {
		snap = *cur
	}
	prevMax := f.maxYear[person]
	if year > prevMax {
		f.maxYear[person] = year
	}
	return snap, prevMax
}

// Apply 把一笔结算的增量写入该（参保人，年度）累计器。
func (f *Fund) Apply(person string, year int, g, x, f1, p, f3 int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	years := f.acc[person]
	cur := years[year]
	if cur == nil {
		cur = &Acc{}
		years[year] = cur
	}
	cur.DU += g
	cur.X += x
	cur.F += f1
	cur.P += p
	cur.Q += f3
}

// Restore 把累计器与最大年度恢复为结算前快照（冲正，O(1)）。
func (f *Fund) Restore(person string, year int, snap Acc, prevMax int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	years := f.acc[person]
	cur := years[year]
	if cur == nil {
		cur = &Acc{}
		years[year] = cur
	}
	*cur = snap
	f.maxYear[person] = prevMax
}

// Snapshot 读取某（参保人，年度）当前累计量（拷贝）。
func (f *Fund) Snapshot(person string, year int) Acc {
	f.mu.Lock()
	defer f.mu.Unlock()
	years := f.acc[person]
	if cur := years[year]; cur != nil {
		return *cur
	}
	return Acc{}
}

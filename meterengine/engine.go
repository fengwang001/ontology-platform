package meterengine

import (
	"math"
	"sort"
	"sync"
)

// Engine 为电表换表与读数衔接引擎。所有方法可并发调用，
// 内部以单一互斥串行化，结果等价于某个串行执行顺序；
// 各操作先完成全部校验再提交，被拒绝时不改变任何状态。
type Engine struct {
	mu     sync.Mutex
	meters map[string]*Meter
	mounts map[string]*Mount
	mhist  map[string][]*Mount
	mmount map[*Meter]*Mount
	trees  map[*Meter]*treap
	maxPer map[string]int64
}

func NewEngine() *Engine {
	return &Engine{
		meters: map[string]*Meter{},
		mounts: map[string]*Mount{},
		mhist:  map[string][]*Mount{},
		mmount: map[*Meter]*Mount{},
		trees:  map[*Meter]*treap{},
		maxPer: map[string]int64{},
	}
}

// RegisterMeter 登记一只电表。digits 为显示位数（1..18），
// 显示值范围 0..10^digits-1；multiplier 必须为正。
func (e *Engine) RegisterMeter(id string, digits int, multiplier int64) error {
	if id == "" {
		return errInvalid("电表标识为空")
	}
	if digits <= 0 || digits > 18 {
		return errInvalid("显示位数 %d 越界", digits)
	}
	if multiplier <= 0 {
		return errInvalid("倍率 %d 非正", multiplier)
	}
	var modulus int64 = 1
	for i := 0; i < digits; i++ {
		modulus *= 10
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.meters[id]; ok {
		return errConflict("电表 %s 已登记", id)
	}
	m := &Meter{id: id, digits: digits, multiplier: multiplier,
		maxDisplay: modulus - 1, modulus: modulus}
	e.meters[id] = m
	e.trees[m] = newTreap()
	return nil
}

// SetPointLimit 设置供电点单位时间最大合理用电量；<=0 表示取消上限。
func (e *Engine) SetPointLimit(point string, maxPerUnit int64) error {
	if point == "" {
		return errInvalid("供电点标识为空")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.maxPer[point] = maxPerUnit
	return nil
}

func (e *Engine) requireMeter(id string) (*Meter, error) {
	m, ok := e.meters[id]
	if !ok {
		return nil, errInvalid("电表 %s 未登记", id)
	}
	return m, nil
}

func checkDisplay(m *Meter, display int64) error {
	if display < 0 || display > m.maxDisplay {
		return errInvalid("电表 %s 显示值 %d 超出 %d 位范围", m.id, display, m.digits)
	}
	return nil
}

func addChecked(a, b int64) (int64, bool) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, false
	}
	return a + b, true
}

func (e *Engine) InstallMeter(point, meterID string, at int64, initialDisplay int64) error {
	if point == "" {
		return errInvalid("供电点标识为空")
	}
	if meterID == "" {
		return errInvalid("电表标识为空")
	}
	if at <= 0 {
		return errInvalid("安装时刻 %d 越界", at)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.requireMeter(meterID)
	if err != nil {
		return err
	}
	if err := checkDisplay(m, initialDisplay); err != nil {
		return err
	}
	if cur := e.mounts[point]; cur != nil {
		return errConflict("供电点 %s 已挂接电表 %s", point, cur.meter.id)
	}
	if _, used := e.mmount[m]; used {
		return errConflict("电表 %s 已有挂接记录", meterID)
	}

	mnt := &Mount{point: point, meter: m, install: at, initial: initialDisplay}
	e.mounts[point] = mnt
	e.mhist[point] = append(e.mhist[point], mnt)
	e.mmount[m] = mnt
	e.trees[m].insert(&Reading{time: at, display: initialDisplay, kind: Actual})
	return nil
}

func (e *Engine) SwapMeter(point string, at int64, oldLastDisplay int64, newMeterID string, newInitialDisplay int64) error {
	if point == "" {
		return errInvalid("供电点标识为空")
	}
	if newMeterID == "" {
		return errInvalid("电表标识为空")
	}
	if at <= 0 {
		return errInvalid("换表时刻 %d 越界", at)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	oldMnt := e.mounts[point]
	if oldMnt == nil {
		return errNotAttached("供电点 %s 无在装电表", point)
	}
	newM, err := e.requireMeter(newMeterID)
	if err != nil {
		return err
	}
	if err := checkDisplay(oldMnt.meter, oldLastDisplay); err != nil {
		return err
	}
	if err := checkDisplay(newM, newInitialDisplay); err != nil {
		return err
	}
	if newM == oldMnt.meter {
		return errConflict("新表不得与旧表相同: %s", newMeterID)
	}
	if _, used := e.mmount[newM]; used {
		return errConflict("电表 %s 已有挂接记录", newMeterID)
	}

	oldTree := e.trees[oldMnt.meter]
	latest := oldTree.latest()
	if at < latest.time {
		return errConflict("换表时刻 %d 早于旧表最新读数时刻 %d", at, latest.time)
	}
	limit := e.maxPer[point]

	var commitOld func()
	if existing := oldTree.find(at); existing != nil {
		if existing.kind == Actual {
			if existing.display != oldLastDisplay {
				return errReadingConflict("时刻 %d 已有实抄值 %d，与 %d 不一致",
					at, existing.display, oldLastDisplay)
			}
			commitOld = func() {}
		} else {
			pred, _ := oldTree.neighbors(at)
			cand := &Reading{time: at, display: oldLastDisplay, kind: Actual}
			if err := validateReplacement(oldMnt.meter, pred, nil, cand, limit); err != nil {
				return err
			}
			commitOld = func() {
				existing.kind = Actual
				existing.display = oldLastDisplay
				existing.fromSwap = true
			}
		}
	} else {
		cand := &Reading{time: at, display: oldLastDisplay, kind: Actual}
		if err := validateSegment(oldMnt.meter, latest, cand, limit); err != nil {
			return err
		}
		commitOld = func() {
			cand.fromSwap = true
			oldTree.insert(cand)
		}
	}

	commitOld()
	oldMnt.remove = at
	newMnt := &Mount{point: point, meter: newM, install: at, initial: newInitialDisplay}
	e.mounts[point] = newMnt
	e.mhist[point] = append(e.mhist[point], newMnt)
	e.mmount[newM] = newMnt
	e.trees[newM].insert(&Reading{time: at, display: newInitialDisplay, kind: Actual, fromSwap: true})
	return nil
}

// validateReplacement 复核替换/乱序后受影响的至多两个邻接区间。
func validateReplacement(m *Meter, pred, succ, cand *Reading, limit int64) error {
	if pred != nil {
		if err := validateSegment(m, pred, cand, limit); err != nil {
			return err
		}
	}
	if succ != nil {
		if err := validateSegment(m, cand, succ, limit); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) RegisterReading(meterID string, at int64, display int64, kind ReadingType) error {
	if meterID == "" {
		return errInvalid("电表标识为空")
	}
	if at <= 0 {
		return errInvalid("读数时刻 %d 越界", at)
	}
	if kind != Actual && kind != Estimated {
		return errInvalid("读数类型 %d 非法", kind)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.requireMeter(meterID)
	if err != nil {
		return err
	}
	if err := checkDisplay(m, display); err != nil {
		return err
	}
	mnt, ok := e.mmount[m]
	if !ok || at < mnt.install || (mnt.remove != 0 && at >= mnt.remove) {
		return errNotAttached("电表 %s 在时刻 %d 不在挂接期内", meterID, at)
	}

	tree := e.trees[m]
	limit := e.maxPer[mnt.point]
	cand := &Reading{time: at, display: display, kind: kind}

	if existing := tree.find(at); existing != nil {
		switch {
		case existing.kind == Actual && kind == Actual:
			if existing.display != display {
				return errReadingConflict("时刻 %d 已有实抄值 %d，新值 %d",
					at, existing.display, display)
			}
			return nil // 幂等
		case existing.kind == Estimated && kind == Estimated:
			return errReadingConflict("时刻 %d 已有估算读数", at)
		default: // 估算被实抄取代，复核两个邻接区间
			pred, succ := tree.neighbors(at)
			if err := validateReplacement(m, pred, succ, cand, limit); err != nil {
				return err
			}
			existing.display = display
			existing.kind = Actual
			return nil
		}
	}

	latest := tree.latest()
	if at < latest.time {
		if kind == Estimated {
			return errConflict("估算读数不得乱序插入：%d 早于最新读数时刻 %d", at, latest.time)
		}
		pred, succ := tree.neighbors(at)
		if err := checkRolloverConservation(m, pred, cand, succ); err != nil {
			return err
		}
		if err := validateReplacement(m, pred, succ, cand, limit); err != nil {
			return err
		}
	} else if err := validateSegment(m, latest, cand, limit); err != nil {
		return err
	}
	tree.insert(cand)
	return nil
}

// checkRolloverConservation 要求拆出的两段翻转次数之和与原区间一致。
func checkRolloverConservation(m *Meter, pred, cand, succ *Reading) error {
	_, oldFlips := segmentDelta(m, pred, succ)
	_, f1 := segmentDelta(m, pred, cand)
	_, f2 := segmentDelta(m, cand, succ)
	if f1+f2 != oldFlips {
		return errConflict("乱序插入翻转不守恒：原区间翻转 %d 次，拆分后 %d+%d 次",
			oldFlips, f1, f2)
	}
	return nil
}

func (e *Engine) ReplaceEstimated(meterID string, at int64, display int64) error {
	return e.RegisterReading(meterID, at, display, Actual)
}

// DeleteEstimated 删除估算读数，前后区间合并后重新判定；不通过则拒绝删除。
func (e *Engine) DeleteEstimated(meterID string, at int64) error {
	if meterID == "" {
		return errInvalid("电表标识为空")
	}
	if at <= 0 {
		return errInvalid("读数时刻 %d 越界", at)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.requireMeter(meterID)
	if err != nil {
		return err
	}
	mnt, ok := e.mmount[m]
	if !ok {
		return errNotAttached("电表 %s 无挂接记录", meterID)
	}
	tree := e.trees[m]
	existing := tree.find(at)
	if existing == nil {
		return errNoReading("电表 %s 在时刻 %d 无读数", meterID, at)
	}
	if existing.kind == Actual {
		return errConflict("电表 %s 在时刻 %d 为实抄读数，不可删除", meterID, at)
	}
	pred, succ := tree.neighbors(at)
	if pred != nil && succ != nil {
		if err := validateSegment(m, pred, succ, e.maxPer[mnt.point]); err != nil {
			return err
		}
	}
	tree.deleteAt(at)
	return nil
}

func (e *Engine) Query(point string, from, to int64) (QueryResult, error) {
	if point == "" {
		return QueryResult{}, errInvalid("供电点标识为空")
	}
	if from <= 0 || to <= 0 {
		return QueryResult{}, errInvalid("查询时刻越界: %d,%d", from, to)
	}
	if from > to {
		return QueryResult{}, errInvalid("查询区间倒置: %d > %d", from, to)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	hist := e.mhist[point]
	if len(hist) == 0 {
		return QueryResult{}, errNoReading("供电点 %s 无挂接记录", point)
	}

	// 起点所在挂接：换表时刻归旧表；终点所在挂接：换表时刻归新表。
	// 挂接历史按安装时刻有序，二分定位，不随历史挂接数线性增长。
	startIdx := sort.Search(len(hist), func(i int) bool { return hist[i].install > from }) - 1
	endIdx := sort.Search(len(hist), func(i int) bool { return hist[i].install > to }) - 1
	if startIdx < 0 || endIdx < 0 || startIdx > endIdx {
		return QueryResult{}, errNoReading("时刻 %d..%d 不落在供电点 %s 的挂接期内", from, to, point)
	}
	startMnt, endMnt := hist[startIdx], hist[endIdx]
	if startMnt.remove != 0 && from >= startMnt.remove {
		return QueryResult{}, errNoReading("时刻 %d 不落在供电点 %s 的挂接期内", from, point)
	}
	if endMnt.remove != 0 && to > endMnt.remove {
		return QueryResult{}, errNoReading("时刻 %d 不落在供电点 %s 的挂接期内", to, point)
	}

	startReading := e.trees[startMnt.meter].find(from)
	endReading := e.trees[endMnt.meter].find(to)
	if startReading == nil {
		return QueryResult{}, errNoReading("时刻 %d 不是供电点 %s 的已有读数时刻", from, point)
	}
	if endReading == nil {
		return QueryResult{}, errNoReading("时刻 %d 不是供电点 %s 的已有读数时刻", to, point)
	}
	if from == to {
		return QueryResult{HasEstimated: startReading.kind == Estimated || endReading.kind == Estimated}, nil
	}

	result := QueryResult{}
	var iterErr error
	addEnergy := func(delta int64, multiplier int64) error {
		part, overflow := mulOverflow(delta, multiplier)
		if overflow {
			return errUnreasonable("用电量累加溢出: %d*%d", delta, multiplier)
		}
		sum, ok := addChecked(result.Energy, part)
		if !ok {
			return errUnreasonable("用电量累加溢出")
		}
		result.Energy = sum
		return nil
	}

	for i := startIdx; i <= endIdx; i++ {
		mnt := hist[i]
		tree := e.trees[mnt.meter]
		lo, hi := from, to
		if i > startIdx {
			lo = mnt.install // 新表链从换表时刻读数起
		}
		if i < endIdx {
			hi = mnt.remove // 旧表链止于换表时刻读数（含）
		}
		var prev *Reading
		segLo, segHi := lo, hi
		// 中间挂接：只取其挂接期 [install,remove] 内读数，
		// 排除因乱序插入而时间戳落在相邻挂接区间的读数（乱序不允许越过换表时刻）。
		if i > startIdx {
			if mnt.install > segLo {
				segLo = mnt.install
			}
		}
		if i < endIdx && mnt.remove < segHi {
			segHi = mnt.remove
		}
		tree.iterateRange(segLo, segHi, func(r *Reading) bool {
			if prev != nil {
				delta, _ := segmentDelta(mnt.meter, prev, r)
				if err := addEnergy(delta, mnt.meter.multiplier); err != nil {
					iterErr = err
					return false
				}
				if prev.kind == Estimated || r.kind == Estimated {
					result.HasEstimated = true
				}
			}
			prev = r
			return r.time < hi
		})
		if iterErr != nil {
			return QueryResult{}, iterErr
		}
	}
	return result, nil
}

package incjoin

import (
	"sort"
	"sync"
)

// Joiner 增量维护两表等值内连接。
//
// 每张表是 (key,value) -> multiplicity（非负 int64）的多重集；
// 连接结果元组 (key,lval,rval) 的重数 = leftMult(key,lval) * rightMult(key,rval)。
//
// Apply 接收一批两侧的带符号变更，校验通过后原子地更新两张表与物化结果，
// 返回连接结果的差分（批后减批前，非零项，按 key/leftVal/rightVal 有序）。
// 任一侧批后出现负重数（删除不存在的行）等非法情况会拒绝整批，状态不变。
//
// 所有方法可被并发调用；Apply 之间串行化（每批是一个原子事务），
// 只读视图与 Apply 并发，读到的永远是某个完整已提交批的状态。
type Joiner struct {
	// mu 为写锁时执行整批校验与提交；为读锁时输出快照。
	mu sync.RWMutex

	// 不变量（在 mu 写锁下维护）：内层 map 只保留正重数的条目，空内层 map 会被删除。
	// left/right: joinKey -> value -> multiplicity（正 int64）。
	left  map[string]map[string]int64
	right map[string]map[string]int64
	// mat: joinKey -> (leftVal,rightVal) -> multiplicity（= 两侧重数之积，正 int64）。
	mat map[string]map[pair]int64
	// totalResult 为 mat 中重数之和（连接结果按重数展开的总元组数）。
	totalResult int64

	// maxResultTuples 为连接结果总元组数（按重数展开的总个数）上限；<=0 表示不限。
	maxResultTuples int64
	logger          Logger
}

// pair 是某个连接键下的 (左值, 右值) 组合。
type pair struct {
	l string
	r string
}

// Logger 记录每次 Apply 的输入、输出差分与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

// Options 为 NewJoiner 的可选项。
type Options struct {
	// MaxResultTuples 限制批后连接结果总元组数（重数之和）；0 表示不限制。
	MaxResultTuples int64
	Logger          Logger
}

// NewJoiner 创建空的连接维护器。
func NewJoiner(opts Options) *Joiner {
	if opts.MaxResultTuples < 0 {
		opts.MaxResultTuples = 0
	}
	return &Joiner{
		left:            make(map[string]map[string]int64),
		right:           make(map[string]map[string]int64),
		mat:             make(map[string]map[pair]int64),
		maxResultTuples: opts.MaxResultTuples,
		logger:          opts.Logger,
	}
}

func (j *Joiner) logf(format string, args ...any) {
	if j.logger != nil {
		j.logger.Logf(format, args...)
	}
}

// Apply 原子地应用一批变更并返回连接结果差分。
// 批被拒绝时返回 *RejectError，且两张表与物化结果均不改变。
func (j *Joiner) Apply(ch Change) (diff []DiffEntry, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.logf("apply begin: left=%s right=%s", formatRows(ch.Left), formatRows(ch.Right))

	// 1) 静态校验：空值与变更符号。逐行、保持输入侧别与顺序，定位首个非法行。
	if err := validateRows("left", ch.Left); err != nil {
		j.logf("apply rejected before any mutation: %v", err)
		return nil, err
	}
	if err := validateRows("right", ch.Right); err != nil {
		j.logf("apply rejected before any mutation: %v", err)
		return nil, err
	}

	// 2) 按 (侧,键,值) 聚合本批增量，随后在基表的浅拷贝上做试应用，
	//    保证任一步失败时真实状态都不被触碰。
	dLeft, dRight, aggErr := aggregate(ch)
	if aggErr != nil {
		j.logf("apply rejected before any mutation: %v", aggErr)
		return nil, aggErr
	}
	lc := cloneGrouped(j.left)
	rc := cloneGrouped(j.right)

	if err := applyDeltas("left", lc, dLeft); err != nil {
		j.logf("apply rejected on trial (state untouched): %v", err)
		return nil, err
	}
	if err := applyDeltas("right", rc, dRight); err != nil {
		j.logf("apply rejected on trial (state untouched): %v", err)
		return nil, err
	}

	// 3) 仅在受影响的连接键上重算连接子多重集，得到差分并检查乘积溢出。
	keys := affectedKeys(dLeft, dRight)
	diff = make([]DiffEntry, 0)
	var deltaTotal int64
	// trialSubs 承载受影响键的试算子多重集；必须为每批调用私有，绝不能跨批/跨 goroutine 共享。
	trialSubs := make(map[string]map[pair]int64, len(keys))

	for _, k := range keys {
		oldSub := j.mat[k]
		newSub, dEntries, subDelta, err := recomputeKey(k, oldSub, lc[k], rc[k])
		if err != nil {
			j.logf("apply rejected on trial (state untouched): %v", err)
			return nil, err
		}
		// deltaTotal 只累加各受影响键的增量（不要把旧总数卷进来）。
		if dt, ok := addChecked(deltaTotal, subDelta); !ok {
			reject := &RejectError{Reason: ReasonMultOverflow, Key: k,
				Msg: "sum of per-key result deltas overflows int64"}
			j.logf("apply rejected on trial (state untouched): %v", reject)
			return nil, reject
		} else {
			deltaTotal = dt
		}
		diff = append(diff, dEntries...)
		// newSub 在提交阶段才写回；这里先放在本批私有的临时映射里。
		trialSubs[k] = newSub
	}

	// 批后总数 = 旧总数 + 增量，此处再做一次溢出检查。
	newTotal, ok := addChecked(j.totalResult, deltaTotal)
	if !ok {
		err := &RejectError{Reason: ReasonMultOverflow,
			Msg: "post-batch total result tuple count overflows int64"}
		j.logf("apply rejected on trial (state untouched): %v", err)
		return nil, err
	}

	// 4) 结果元组数上限校验（按重数展开计数）。
	if j.maxResultTuples > 0 && newTotal > j.maxResultTuples {
		err := &RejectError{
			Reason: ReasonResultLimitExceeded,
			Msg:    formatTotalLimit(j.totalResult, deltaTotal, j.maxResultTuples),
		}
		j.logf("apply rejected on trial (state untouched): %v", err)
		return nil, err
	}

	// 5) 全部校验通过：提交（试算拷贝转正，物化结果按键整体替换）。
	j.left = lc
	j.right = rc
	for _, k := range keys {
		newSub := trialSubs[k]
		if len(newSub) == 0 {
			delete(j.mat, k)
		} else {
			j.mat[k] = newSub
		}
	}
	j.totalResult = newTotal

	sortDiff(diff)
	j.logf("apply committed: delta_total=%d total=%d diff=%s | basis: validation ok, "+
		"post-batch multiplicities nonnegative, result limit ok",
		deltaTotal, j.totalResult, formatEntries(diff))
	return diff, nil
}

// Snapshot 返回当前连接结果全量快照，按 key/leftVal/rightVal 有序，重数均为正。
// 与 Apply 并发时反映某个完整已提交状态，绝不会出现半批或负重数。
func (j *Joiner) Snapshot() []DiffEntry {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.snapshotLocked()
}

// snapshotLocked 返回物化结果的全量有序快照，调用方必须持有读锁或写锁。
func (j *Joiner) snapshotLocked() []DiffEntry {
	out := make([]DiffEntry, 0)
	for k, sub := range j.mat {
		for p, m := range sub {
			out = append(out, DiffEntry{Key: k, LeftVal: p.l, RightVal: p.r, Mult: m})
		}
	}
	sortDiff(out)
	return out
}

// LeftSnapshot / RightSnapshot 返回两侧基表的当前全量多重集（调试/校验用），
// 按 key/value 有序，Mult 为当前非负重数。
func (j *Joiner) LeftSnapshot() []Row {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return tableSnapshot(j.left)
}

func (j *Joiner) RightSnapshot() []Row {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return tableSnapshot(j.right)
}

// ---- 试算与重算 ----

// validateRows 检查单侧输入行的空值与变更符号，返回首个非法行对应的拒绝错误。
func validateRows(side string, rows []Row) error {
	for _, r := range rows {
		if r.Key == "" {
			return &RejectError{Reason: ReasonEmptyKey, Side: side, Key: r.Key, Val: r.Value,
				Msg: side + " row has empty key (null keys are not allowed)"}
		}
		if r.Value == "" {
			return &RejectError{Reason: ReasonEmptyValue, Side: side, Key: r.Key, Val: r.Value,
				Msg: side + " row has empty value (null values are not allowed)"}
		}
		if r.Mult == 0 {
			return &RejectError{Reason: ReasonInvalidMultSign, Side: side, Key: r.Key, Val: r.Value,
				Msg: side + " row multiplicity delta must be positive (insert) or negative (delete), got 0"}
		}
	}
	return nil
}

// aggregate 把一批行按 (key,value) 求和，返回两侧的聚合增量。
// 同一 (key,value) 的多行增量之和若溢出 int64，返回 MULTIPLICITY_OVERFLOW。
func aggregate(ch Change) (map[string]map[string]int64, map[string]map[string]int64, error) {
	dl := make(map[string]map[string]int64)
	dr := make(map[string]map[string]int64)
	add := func(side string, dst map[string]map[string]int64, r Row) error {
		sub := dst[r.Key]
		if sub == nil {
			sub = make(map[string]int64)
			dst[r.Key] = sub
		}
		s, ok := addChecked(sub[r.Value], r.Mult)
		if !ok {
			return &RejectError{Reason: ReasonMultOverflow, Side: side, Key: r.Key, Val: r.Value,
				Msg: side + " in-batch aggregated delta overflows int64"}
		}
		sub[r.Value] = s
		return nil
	}
	for _, r := range ch.Left {
		if err := add("left", dl, r); err != nil {
			return nil, nil, err
		}
	}
	for _, r := range ch.Right {
		if err := add("right", dr, r); err != nil {
			return nil, nil, err
		}
	}
	return dl, dr, nil
}

// cloneGrouped 对嵌套 map 做浅拷贝（内层 map 也复制），供试算改副本而不动真实状态。
func cloneGrouped(src map[string]map[string]int64) map[string]map[string]int64 {
	dst := make(map[string]map[string]int64, len(src))
	for k, sub := range src {
		cp := make(map[string]int64, len(sub))
		for v, m := range sub {
			cp[v] = m
		}
		dst[k] = cp
	}
	return dst
}

// applyDeltas 把聚合增量试应用到基表副本上：批后重数为负即拒绝；为 0 则删除条目。
func applyDeltas(side string, tbl map[string]map[string]int64, deltas map[string]map[string]int64) error {
	for k, dvals := range deltas {
		sub := tbl[k]
		if sub == nil {
			sub = make(map[string]int64)
			tbl[k] = sub
		}
		for v, d := range dvals {
			// 聚合后的单条增量仍可能为 0（同行 +1/-1 抵消）——对状态无影响，跳过。
			if d == 0 {
				continue
			}
			newM, ok := addChecked(sub[v], d)
			if !ok {
				return &RejectError{Reason: ReasonMultOverflow, Side: side, Key: k, Val: v,
					Msg: side + " multiplicity overflows int64"}
			}
			if newM < 0 {
				return &RejectError{Reason: ReasonDeleteNonexistent, Side: side, Key: k, Val: v,
					Msg: formatDeleteMsg(side, k, v, sub[v], d)}
			}
			if newM == 0 {
				delete(sub, v)
			} else {
				sub[v] = newM
			}
		}
		if len(sub) == 0 {
			delete(tbl, k)
		}
	}
	return nil
}

// affectedKeys 返回本批触及的连接键（两侧增量键的并集），有序。
func affectedKeys(dl, dr map[string]map[string]int64) []string {
	seen := make(map[string]struct{}, len(dl)+len(dr))
	for k := range dl {
		seen[k] = struct{}{}
	}
	for k := range dr {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// recomputeKey 重算单个连接键批后的连接子多重集，并与批前 oldSub 求差。
// 返回新子多重集（空 map 表示该键不再有结果）、非零差分条目、重数总和增量。
func recomputeKey(k string, oldSub map[pair]int64,
	newLeft, newRight map[string]int64) (map[pair]int64, []DiffEntry, int64, error) {

	newSub := make(map[pair]int64)
	var newSum int64
	// 任一子表为空时连接结果为空，直接得到全删/全增差分。
	if len(newLeft) > 0 && len(newRight) > 0 {
		for lv, lm := range newLeft {
			for rv, rm := range newRight {
				m, ok := mulChecked(lm, rm)
				if !ok {
					return nil, nil, 0, &RejectError{Reason: ReasonMultOverflow, Key: k,
						Msg: formatMulMsg(k, lv, rv, lm, rm)}
				}
				newSub[pair{l: lv, r: rv}] = m
				s, ok := addChecked(newSum, m)
				if !ok {
					return nil, nil, 0, &RejectError{Reason: ReasonMultOverflow, Key: k,
						Msg: "per-key result tuple count overflows int64"}
				}
				newSum = s
			}
		}
	}

	var oldSum int64
	for _, m := range oldSub {
		oldSum += m
	}

	entries := make([]DiffEntry, 0)
	// 批后存在的元组：新增或重数变化。
	for p, nm := range newSub {
		if om := oldSub[p]; om != nm {
			entries = append(entries, DiffEntry{Key: k, LeftVal: p.l, RightVal: p.r, Mult: nm - om})
		}
	}
	// 批前存在、批后消失的元组：差分 -om。
	for p, om := range oldSub {
		if _, ok := newSub[p]; !ok {
			entries = append(entries, DiffEntry{Key: k, LeftVal: p.l, RightVal: p.r, Mult: -om})
		}
	}
	return newSub, entries, newSum - oldSum, nil
}

// addChecked 计算 a+b，溢出时 ok=false。
func addChecked(a, b int64) (int64, bool) {
	s := a + b
	if (b > 0 && s < a) || (b < 0 && s > a) {
		return 0, false
	}
	return s, true
}

// mulChecked 计算 a*b，溢出时 ok=false。
func mulChecked(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	p := a * b
	if p/a != b {
		return 0, false
	}
	return p, true
}

// tableSnapshot 输出一侧基表的全量有序行。
func tableSnapshot(tbl map[string]map[string]int64) []Row {
	out := make([]Row, 0)
	for k, sub := range tbl {
		for v, m := range sub {
			out = append(out, Row{Key: k, Value: v, Mult: m})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// sortDiff 按 key、leftVal、rightVal 排序（原地）。
func sortDiff(d []DiffEntry) {
	sort.Slice(d, func(i, j int) bool {
		if d[i].Key != d[j].Key {
			return d[i].Key < d[j].Key
		}
		if d[i].LeftVal != d[j].LeftVal {
			return d[i].LeftVal < d[j].LeftVal
		}
		return d[i].RightVal < d[j].RightVal
	})
}

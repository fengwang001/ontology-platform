package join

import (
	"log/slog"
	"math/bits"
	"sort"
	"sync"
)

// DefaultMaxResultTuples 是未显式配置限制时，物化连接结果允许的不同元组数上限。
const DefaultMaxResultTuples int64 = 1_000_000

// Options 控制 Joiner 的行为。
type Options struct {
	// MaxResultTuples 限制物化连接结果中不同元组（JoinTuple）的数量；<=0 表示使用默认值。
	MaxResultTuples int64
	// Logger 用于打印每批的输入、输出差分与判定依据；nil 时使用写 stderr 的默认 logger。
	Logger *slog.Logger
}

// Joiner 是两表等值内连接的增量差分维护器。
//
// 状态由一把 RWMutex 保护：Apply 串行化（写锁），Snapshot 可并发（读锁）。
// 拒绝的批次在任何状态修改之前返回，因此绝不留下半批或负重数。
type Joiner struct {
	mu sync.RWMutex

	// seq 为每次 Apply 调用分配的单调序号（含被拒绝批次），仅用于日志关联。
	seq int64

	left   map[Row]int64
	right  map[Row]int64
	result map[JoinTuple]int64

	maxResultTuples int64
	logger          *applyLogger
}

// New 创建一个左表、右表与物化结果均为空的维护器。
func New(opts Options) *Joiner {
	max := opts.MaxResultTuples
	if max <= 0 {
		max = DefaultMaxResultTuples
	}
	return &Joiner{
		left:            map[Row]int64{},
		right:           map[Row]int64{},
		result:          map[JoinTuple]int64{},
		maxResultTuples: max,
		logger:          newApplyLogger(opts.Logger),
	}
}

// Apply 原子地校验并应用一个批次：
//   - 校验通过：更新两张表与物化结果，返回按 (Key,LeftValue,RightValue) 有序、
//     只含非零重数的差分；下游把差分顺序累加到上一批结果上即得当前完整连接结果。
//   - 校验失败：返回结构化拒绝原因，两张表与物化结果保持批前状态（逐字节不变）。
//
// 连接重数定义：结果元组 (k, lv, rv) 的重数 = 左表行 (k, lv) 重数 × 右表行 (k, rv) 重数。
func (j *Joiner) Apply(b Batch) BatchResult {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.seq++
	seq := j.seq
	j.logger.logStart(seq, b)

	// ---- 阶段 1：校验并聚合两侧变更（不修改任何状态） ----
	leftDelta, leftOrder, err := j.validateSide("left", b.Left, j.left)
	if err != nil {
		return j.reject(seq, b, err)
	}
	rightDelta, rightOrder, err := j.validateSide("right", b.Right, j.right)
	if err != nil {
		return j.reject(seq, b, err)
	}

	// ---- 阶段 2：在副本上推演连接差分（仍不修改可见状态） ----
	deltas, born, died, err := j.computeDeltas(leftDelta, leftOrder, rightDelta, rightOrder)
	if err != nil {
		return j.reject(seq, b, err)
	}

	// 结果元组数限制按提交后的不同元组数计。
	postCount := int64(len(j.result)) + int64(born) - int64(died)
	if postCount > j.maxResultTuples {
		return j.reject(seq, b, reject(ReasonResultTooLarge, "", "", "",
			"post-batch distinct result tuples "+itoa(postCount)+" exceeds limit "+itoa(j.maxResultTuples)))
	}

	// ---- 阶段 3：全部校验通过，提交（此阶段不可能失败） ----
	j.commit(leftDelta, rightDelta, deltas)

	sort.Slice(deltas, func(a, b int) bool { return deltas[a].JoinTuple.less(deltas[b].JoinTuple) })
	j.logger.logAccepted(seq, b, deltas, len(j.result))
	return BatchResult{Accepted: true, Deltas: deltas}
}

// validateSide 校验单侧变更并聚合为“每行净变更”，同时保留首次出现顺序以保证错误信息确定。
//
// 校验顺序固定：先按输入顺序做空值与零增量校验，再按首次出现顺序做批后负重数校验。
// 返回的 map 仅含净变更非零的行。
func (j *Joiner) validateSide(side string, changes []RowChange, cur map[Row]int64) (map[Row]int64, []Row, *RejectError) {
	deltas := make(map[Row]int64, len(changes))
	order := make([]Row, 0, len(changes))

	// 第一遍：空值、零增量、聚合（含溢出检查）。
	for _, c := range changes {
		if c.Key == "" || c.Value == "" {
			return nil, nil, reject(ReasonNullValue, side, c.Key, c.Value,
				"join key and values must not be NULL")
		}
		if c.Delta == 0 {
			return nil, nil, reject(ReasonInvalidDelta, side, c.Key, c.Value,
				"multiplicity delta must be non-zero (positive for insert, negative for delete)")
		}
		r := c.Row()
		if _, seen := deltas[r]; !seen {
			order = append(order, r)
		}
		sum, ok := addChecked(deltas[r], c.Delta)
		if !ok {
			return nil, nil, reject(ReasonMultiplicityOverflow, side, c.Key, c.Value,
				"per-row delta aggregation overflows int64")
		}
		deltas[r] = sum
	}

	// 第二遍：批后负重数（删除不存在的行）与溢出，按首次出现顺序报告。
	filtered := make([]Row, 0, len(order))
	for _, r := range order {
		d := deltas[r]
		if d == 0 {
			delete(deltas, r) // 同批内同增同减抵消的行不参与后续计算。
			continue
		}
		filtered = append(filtered, r)
		newMult, ok := addChecked(cur[r], d)
		if !ok {
			return nil, nil, reject(ReasonMultiplicityOverflow, side, r.Key, r.Value,
				"post-batch row multiplicity overflows int64")
		}
		if newMult < 0 {
			return nil, nil, reject(ReasonDeleteNonexistent, side, r.Key, r.Value,
				"post-batch multiplicity would be "+itoa(newMult)+" (current "+itoa(cur[r])+", delta "+itoa(d)+")")
		}
	}
	return deltas, filtered, nil
}

// rowMult 是按键建索引时的一条（行, 重数）。
type rowMult struct {
	row  Row
	mult int64
}

// computeDeltas 依据两侧净变更推演本批的连接结果差分。
//
// 对受影响连接键 k，差分来自两部分：
//
//	(lv 变更)  Δ = (m_l_new - m_l_old) * m_r_old
//	(rv 变更)  Δ = m_l_new       * (m_r_new - m_r_old)
//
// 第一项让变更左行与“批前全部”右行（旧重数）配对；第二项让变更右行与
// “批后全部”左行（新重数，即旧左行与本批变更左行的并集）配对。
// 于是每对 (lv, rv) 恰被计算一次，且同批左右都变更时 (新 lv, 新 rv) 也不会遗漏。
//
// born/died 分别统计差分后从无到有 / 从有到无的不同结果元组数，用于超限判定。
// 返回的 deltas 只含净差分非零的元组，顺序未定（Apply 负责排序）。
func (j *Joiner) computeDeltas(
	leftDelta map[Row]int64, leftOrder []Row,
	rightDelta map[Row]int64, rightOrder []Row,
) (deltas []JoinDelta, born, died int, err *RejectError) {
	out := map[JoinTuple]int64{}

	addContribution := func(t JoinTuple, contribution int64) bool {
		sum, ok := addChecked(out[t], contribution)
		if !ok {
			err = reject(ReasonMultiplicityOverflow, "", t.Key, "",
				"join result multiplicity delta overflows int64 at tuple "+t.String())
			return false
		}
		out[t] = sum
		return true
	}

	// 为两张表按连接键建一次性索引（值有序，保证即便不经最终排序也可预测）。
	leftByKey := indexByKey(j.left)
	rightByKey := indexByKey(j.right)

	// 受影响键集合（差分只可能发生在这些键上），有序遍历。
	keys := make(map[string]struct{}, len(leftOrder)+len(rightOrder))
	for _, r := range leftOrder {
		keys[r.Key] = struct{}{}
	}
	for _, r := range rightOrder {
		keys[r.Key] = struct{}{}
	}
	keyOrder := make([]string, 0, len(keys))
	for k := range keys {
		keyOrder = append(keyOrder, k)
	}
	sort.Strings(keyOrder)

	for _, k := range keyOrder {
		changedL := rowsOnKey(leftOrder, k)
		changedR := rowsOnKey(rightOrder, k)

		// 第一项：每个变更左行 × 批前同键全部右行（旧重数）。
		for _, lr := range changedL {
			dl := leftDelta[lr] // 非零
			for _, rm := range rightByKey[k] {
				prod, ok := mulChecked(dl, rm.mult)
				if !ok {
					return nil, 0, 0, reject(ReasonMultiplicityOverflow, "left", lr.Key, lr.Value,
						"product of multiplicities overflows int64")
				}
				if prod == 0 {
					continue
				}
				t := JoinTuple{Key: k, LeftValue: lr.Value, RightValue: rm.row.Value}
				if !addContribution(t, prod) {
					return nil, 0, 0, err
				}
			}
		}

		// 批后同键全部左行（值 -> 新重数）：旧左行并入，再用变更左行覆盖。
		newLeft := make(map[string]int64, len(leftByKey[k])+len(changedL))
		for _, lm := range leftByKey[k] {
			newLeft[lm.row.Value] = lm.mult
		}
		for _, lr := range changedL {
			newLeft[lr.Value] = j.left[lr] + leftDelta[lr] // validateSide 已保证不溢出且非负
		}

		// 第二项：每个变更右行 × 批后同键全部左行（新重数）。
		for _, rr := range changedR {
			dr := rightDelta[rr] // 非零
			for lv, newLMult := range newLeft {
				prod, ok := mulChecked(newLMult, dr)
				if !ok {
					return nil, 0, 0, reject(ReasonMultiplicityOverflow, "right", rr.Key, rr.Value,
						"product of multiplicities overflows int64")
				}
				if prod == 0 {
					continue
				}
				t := JoinTuple{Key: k, LeftValue: lv, RightValue: rr.Value}
				if !addContribution(t, prod) {
					return nil, 0, 0, err
				}
			}
		}
	}

	for t, d := range out {
		if d == 0 {
			delete(out, t)
			continue
		}
		old := j.result[t]
		newMult, ok := addChecked(old, d)
		if !ok {
			return nil, 0, 0, reject(ReasonMultiplicityOverflow, "", t.Key, "",
				"post-batch join multiplicity overflows int64 at tuple "+t.String())
		}
		if newMult < 0 {
			return nil, 0, 0, reject(ReasonMultiplicityOverflow, "", t.Key, "",
				"post-batch join multiplicity would be negative at tuple "+t.String())
		}
		if old == 0 && newMult > 0 {
			born++
		}
		if old > 0 && newMult == 0 {
			died++
		}
		deltas = append(deltas, JoinDelta{JoinTuple: t, Delta: d})
	}
	return deltas, born, died, nil
}

// indexByKey 把表组织为 键 -> 同键行列表（按值有序）。
func indexByKey(m map[Row]int64) map[string][]rowMult {
	idx := make(map[string][]rowMult, len(m))
	for r, mult := range m {
		idx[r.Key] = append(idx[r.Key], rowMult{row: r, mult: mult})
	}
	for k := range idx {
		sort.Slice(idx[k], func(a, b int) bool { return idx[k][a].row.Value < idx[k][b].row.Value })
	}
	return idx
}

// rowsOnKey 返回 rows 中键为 k 的行，保持传入顺序。
func rowsOnKey(rows []Row, k string) []Row {
	var out []Row
	for _, r := range rows {
		if r.Key == k {
			out = append(out, r)
		}
	}
	return out
}

// commit 在所有校验通过后把变更落入两张表与物化结果。
func (j *Joiner) commit(leftDelta, rightDelta map[Row]int64, deltas []JoinDelta) {
	for r, d := range leftDelta {
		applyRow(j.left, r, d)
	}
	for r, d := range rightDelta {
		applyRow(j.right, r, d)
	}
	for _, d := range deltas {
		applyTuple(j.result, d.JoinTuple, d.Delta)
	}
}

// applyRow 把净变更并入单侧表，归零即删除条目。
func applyRow(m map[Row]int64, r Row, d int64) {
	nv := m[r] + d
	if nv == 0 {
		delete(m, r)
		return
	}
	m[r] = nv
}

// applyTuple 把差分并入物化结果，归零即删除条目。
func applyTuple(m map[JoinTuple]int64, t JoinTuple, d int64) {
	nv := m[t] + d
	if nv == 0 {
		delete(m, t)
		return
	}
	m[t] = nv
}

// reject 统一记录拒绝日志并返回拒绝结果；调用点尚未修改任何状态。
func (j *Joiner) reject(seq int64, b Batch, err *RejectError) BatchResult {
	j.logger.logRejected(seq, b, err)
	return BatchResult{Accepted: false, Err: err}
}

// Snapshot 返回当前左表、右表与物化连接结果的一致深拷贝。
//
// 持读锁期间三份状态来自同一个已提交批次；并发调用 Apply 时不会读到半批。
// 返回的 map 归调用方所有，可自由修改、与 FullJoin 的全量重算结果逐键比较。
func (j *Joiner) Snapshot() (left, right map[Row]int64, result map[JoinTuple]int64) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return copyRows(j.left), copyRows(j.right), copyTuples(j.result)
}

func copyRows(src map[Row]int64) map[Row]int64 {
	dst := make(map[Row]int64, len(src))
	for r, m := range src {
		dst[r] = m
	}
	return dst
}

func copyTuples(src map[JoinTuple]int64) map[JoinTuple]int64 {
	dst := make(map[JoinTuple]int64, len(src))
	for t, m := range src {
		dst[t] = m
	}
	return dst
}

// FullJoin 对给定的两张多重集表做一次性全量等值内连接重算。
//
// 结果元组 (k, lv, rv) 的重数等于两侧行重数之积；负重数输入视为调用方错误，
// 该函数不做校验（维护器中的状态恒非负）。它是增量物化结果的参照真值。
func FullJoin(left, right map[Row]int64) map[JoinTuple]int64 {
	out := map[JoinTuple]int64{}
	// 按右行组织，避免无关节的嵌套扫描。
	byKey := map[string]map[string]int64{}
	for r, m := range right {
		if byKey[r.Key] == nil {
			byKey[r.Key] = map[string]int64{}
		}
		byKey[r.Key][r.Value] += m
	}
	for lr, ml := range left {
		for rv, mr := range byKey[lr.Key] {
			out[JoinTuple{Key: lr.Key, LeftValue: lr.Value, RightValue: rv}] = ml * mr
		}
	}
	return out
}

const (
	maxInt64 = int64(^uint64(0) >> 1)
	minInt64 = -maxInt64 - 1
)

// addChecked 返回 a+b；溢出时 ok=false。
func addChecked(a, b int64) (int64, bool) {
	s := a + b
	if (a > 0 && b > 0 && s < 0) || (a < 0 && b < 0 && s >= 0) {
		return 0, false
	}
	return s, true
}

// mulChecked 返回 a*b；乘积超出 int64 范围时 ok=false。
func mulChecked(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	// 绝对值（无符号），MinInt64 的绝对值恰为 1<<63，仍可由 uint64 表示。
	ua, ub := uint64(a), uint64(b)
	if a < 0 {
		ua = -ua
	}
	if b < 0 {
		ub = -ub
	}
	hi, lo := bits.Mul64(ua, ub)
	neg := (a < 0) != (b < 0)
	const signBit = uint64(1) << 63
	// 幅度必须 <= MaxInt64；等于 1<<63 时仅在结果为负（MinInt64）时可表示。
	if hi != 0 || lo > signBit || (lo == signBit && !neg) {
		return 0, false
	}
	p := int64(lo) // lo==signBit 时 int64(lo) == MinInt64
	if neg {
		if lo == signBit {
			return p, true // -2^63 即可表示的 MinInt64
		}
		return -p, true
	}
	return p, true
}

// itoa 避免在本包额外引入 strconv 到核心文件的轻量整数格式化。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [24]byte
	i := len(buf)
	neg := n < 0
	u := uint64(n)
	if neg {
		u = -u
	}
	for u > 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

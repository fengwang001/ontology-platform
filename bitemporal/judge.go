package bitemporal

import "fmt"

// Status 是单条记录兼容性判定的结论类别。
type Status string

const (
	StatusCompatible     Status = "compatible"
	StatusInfoLoss       Status = "info_loss"
	StatusIncompatible   Status = "incompatible"
	StatusRecordInvalid  Status = "record_invalid"
	StatusUnknownVersion Status = "unknown_version"
)

// LostAxis 描述一条被丢弃的时间轴。
type LostAxis struct {
	Axis   Axis
	Reason string
}

// FilledAxis 描述一条按固定规则填充的时间轴。
type FilledAxis struct {
	Axis   Axis
	Reason string
}

// Verdict 是单条记录的完整判定结果。
//
// 四类互不相同的结论及其固定判定优先级（数字越小越先判定）：
//  1. record_invalid：记录自身时态区间不自洽（起点晚于终点等）。
//     记录自身的错误与格式版本无关，必须先于一切格式判定暴露，
//     否则同一条坏记录在不同版本对下会得到不同结论，既不稳定也误导使用者。
//  2. incompatible：源/目标都记录的时间轴上，边界闭合/开放语义差异会使
//     同一查询时点在两种格式下归属判定不同。归属语义改变无法靠丢弃信息
//     解决，属于硬性不兼容，故先于信息损失判定。
//  3. info_loss：仅由“某条时间轴记录与否”的差异引起，目标格式不再记录
//     源格式记录的轴。轴可以被完整丢弃并明确报告丢了哪条轴，不影响
//     其余轴的查询正确性，因此是损失而非不兼容。
//  4. unknown_version：源或目标版本号超出注册表可识别范围。版本识别是
//     查表动作，前三级都不依赖版本以外的外部资源即可独立判定，故置于
//     最后；这样坏记录不会因为“版本也不认识”而被掩盖。
type Verdict struct {
	RecordID string
	Status   Status
	Lost     []LostAxis
	Filled   []FilledAxis
	Boundary []BoundaryVerdict
	Reasons  []string
}

var allAxes = []Axis{ValidTime, TransactionTime}

func axisPresent(r *Record, a Axis) bool {
	switch a {
	case ValidTime:
		return r.Valid != nil
	case TransactionTime:
		return r.Transaction != nil
	}
	return false
}

func axisInterval(r *Record, a Axis) *Interval {
	switch a {
	case ValidTime:
		return r.Valid
	case TransactionTime:
		return r.Transaction
	}
	return nil
}

// Judge 对单条记录执行从 src 到 dst 的兼容性判定。
//
// Judge 是纯函数：不修改被判定记录，不修改注册表，不维护任何可变状态，
// 因而对同一条记录并发多次调用必然得到完全一致的结果。
func Judge(reg *Registry, r *Record, src, dst string) Verdict {
	v := Verdict{RecordID: r.ID}

	// 优先级 1：记录自身时态区间自洽性（先于版本识别）。
	if err := r.Validate(); err != nil {
		v.Status = StatusRecordInvalid
		v.Reasons = append(v.Reasons, err.Error())
		return v
	}

	// 优先级 4（查表先做，分类结论最后给）：识别源/目标版本。
	srcVer, srcOK := reg.Get(src)
	dstVer, dstOK := reg.Get(dst)
	if !srcOK || !dstOK {
		v.Status = StatusUnknownVersion
		switch {
		case !srcOK && !dstOK:
			v.Reasons = append(v.Reasons, fmt.Sprintf("unknown source version %q and unknown target version %q", src, dst))
		case !srcOK:
			v.Reasons = append(v.Reasons, fmt.Sprintf("unknown source version %q", src))
		default:
			v.Reasons = append(v.Reasons, fmt.Sprintf("unknown target version %q", dst))
		}
		return v
	}

	// 数据与源版本声明必须一致：源版本声称记录的轴在记录中缺失，
	// 属于记录与所声明格式不符，归入记录自身问题（优先级 1 的延伸）。
	for _, a := range allAxes {
		spec := srcVer.AxisSpecOf(a)
		if spec.Recorded && !axisPresent(r, a) {
			v.Status = StatusRecordInvalid
			v.Reasons = append(v.Reasons, fmt.Sprintf("record claims source version %s which records %s, but the interval is absent", src, a))
			return v
		}
	}

	// 优先级 2：源、目标都记录的轴，比较边界语义是否改变查询归属。
	for _, a := range allAxes {
		srcSpec := srcVer.AxisSpecOf(a)
		dstSpec := dstVer.AxisSpecOf(a)
		if !srcSpec.Recorded || !dstSpec.Recorded {
			continue
		}
		bv := CompareBoundary(a, srcSpec, dstSpec, *axisInterval(r, a))
		v.Boundary = append(v.Boundary, bv)
		if bv.Differs {
			v.Status = StatusIncompatible
			detail := bv.Reason
			if bv.HasWitness {
				detail = fmt.Sprintf("%s (witness time: %d)", bv.Reason, bv.Witness)
			}
			v.Reasons = append(v.Reasons, fmt.Sprintf("%s: %s", a, detail))
		}
	}
	if v.Status == StatusIncompatible {
		return v
	}

	// 优先级 3：轴记录与否的差异。
	for _, a := range allAxes {
		srcSpec := srcVer.AxisSpecOf(a)
		dstSpec := dstVer.AxisSpecOf(a)
		switch {
		case srcSpec.Recorded && !dstSpec.Recorded:
			v.Lost = append(v.Lost, LostAxis{
				Axis:   a,
				Reason: fmt.Sprintf("target version %s does not record %s; the whole axis is dropped", dst, a),
			})
		case !srcSpec.Recorded && dstSpec.Recorded:
			v.Filled = append(v.Filled, FilledAxis{
				Axis:   a,
				Reason: defaultFillRule(a),
			})
		}
	}
	if len(v.Lost) > 0 {
		v.Status = StatusInfoLoss
		for _, l := range v.Lost {
			v.Reasons = append(v.Reasons, string(l.Axis)+": "+l.Reason)
		}
	} else {
		v.Status = StatusCompatible
	}
	for _, f := range v.Filled {
		v.Reasons = append(v.Reasons, string(f.Axis)+": "+f.Reason)
	}
	return v
}

// JudgeBatch 对一批记录执行判定。
//
// 每条记录独立判定、互不影响；每条记录做 O(1) 的工作（轴只有两条、
// 区间比较为常数时间），故总开销为 O(n)，n 为记录总数。
func JudgeBatch(reg *Registry, rs []*Record, src, dst string) []Verdict {
	out := make([]Verdict, len(rs))
	for i, r := range rs {
		out[i] = Judge(reg, r, src, dst)
	}
	return out
}

// defaultFillRule 返回“源不记录、目标记录”的轴的固定填充规则说明。
//
// 规则全局唯一、不随实例变化、不允许逐例人工指定：
// 缺失轴一律填充为“覆盖全部业务时间”的永恒区间
// [MinFinite, MaxFinite+1)（在目标格式为闭区间时等价记为
// [MinFinite, MaxFinite]）。语义为“该轴上信息未知 ⇒ 视为对所有时点成立”，
// 与快照只记录当前已知事实的解释一致；该规则是确定的、可复核的。
func defaultFillRule(a Axis) string {
	return fmt.Sprintf("source does not record %s; filled deterministically with the eternity interval [%d, %d) (all business times)", a, MinFinite, MaxFinite+1)
}

// DefaultInterval 按目标版本的边界约定构造某条缺失轴的固定填充区间。
func DefaultInterval(dst AxisSpec) Interval {
	if dst.EndBound == Closed {
		return Interval{Start: MinFinite, End: MaxFinite, StartBound: Closed, EndBound: Closed}
	}
	return Interval{Start: MinFinite, End: MaxFinite + 1, StartBound: Closed, EndBound: HalfOpen}
}

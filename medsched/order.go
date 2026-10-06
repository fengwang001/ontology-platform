package medsched

// gen 是固定间隔医嘱的一个“代”：补给成功后开启新一代。
type gen struct {
	anchor  int64 // 该代第一个计划点时刻（补给时刻 + H）
	cutoffK int64 // 已关闭代中，targetK 之后的点作废；活动代为 maxInt64
}

// order 是一张医嘱的全部运行时状态。
type order struct {
	id        string
	patient   string
	drug      string
	freq      Frequency
	openedAt  int64
	stoppedAt int64 // 0 表示在途；否则为停嘱时刻

	// 固定间隔的“代”。第 0 代 anchor = freq.First。
	gens []gen

	// 固定时点医嘱的已排序日内时点（开立时固化）。
	dailyTimes []int64

	// 已处理记录按“代”存储：固定间隔每代一份；固定时点统一存于下标 0。
	// 每代内按计划时刻升序。重排后旧点与新点可能同一时刻，必须按代区分。
	genRecords [][]record

	// 必要时（PRN）的实际给药时刻，升序。
	prnDoses []int64
}

// record 是一个计划点被处理后的不可变记录。
type record struct {
	at   int64 // 计划时刻
	done int64 // 实际处理时刻（给药或拒服）
	kind recordKind
}

type recordKind int

const (
	rkOnTime  recordKind = iota + 1 // 按时给药
	rkRefused                       // 拒服
	rkMadeUp                        // 漏给后补给
)

// activeGenIdx 返回当前活动代下标。
func (o *order) activeGenIdx() int { return len(o.gens) - 1 }

// findIn 在某代记录中二分查找计划时刻 t，返回下标，不存在返回 -1。
func findIn(rs []record, t int64) int {
	lo, hi := 0, len(rs)
	for lo < hi {
		mid := (lo + hi) / 2
		if rs[mid].at < t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(rs) && rs[lo].at == t {
		return lo
	}
	return -1
}

// insertRecord 按计划时刻升序插入一条记录。
func insertRecord(rs []record, r record) []record {
	idx := len(rs)
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i].at <= r.at {
			break
		}
		idx = i
	}
	rs = append(rs, record{})
	copy(rs[idx+1:], rs[idx:])
	rs[idx] = r
	return rs
}

// isStopped 报告医嘱在给定时刻是否已停（now >= 停嘱时刻）。
func (o *order) isStopped(now int64) bool { return o.stoppedAt != 0 && now >= o.stoppedAt }

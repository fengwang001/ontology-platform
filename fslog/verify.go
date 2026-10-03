package fslog

import "strconv"

// Category 是校验结论类别。
type Category uint8

const (
	// CatOK 表示整段校验通过。
	CatOK Category = iota
	// CatInvalidParam 表示检查点集合非法（为空等）。
	CatInvalidParam
	// CatGap 表示条目 Index 大于所在位置（缺口）。
	CatGap
	// CatReplay 表示条目 Index 小于所在位置（回放）。
	CatReplay
	// CatAppendAfterSeal 表示封存记录之后仍有条目。
	CatAppendAfterSeal
	// CatTimeRegression 表示时间戳小于前一条。
	CatTimeRegression
	// CatTamper 表示标记不等或类型非法（篡改）。
	CatTamper
	// CatContentMismatch 表示封存/换钥记录的 Data 不是序号的十进制文本。
	CatContentMismatch
	// CatCheckpointConflict 表示检查点密钥与推导密钥不一致。
	CatCheckpointConflict
	// CatTailTruncation 表示存在序号大于条目数的检查点（尾部截断）。
	CatTailTruncation
)

func (c Category) String() string {
	switch c {
	case CatOK:
		return "通过"
	case CatInvalidParam:
		return "参数非法"
	case CatGap:
		return "缺口"
	case CatReplay:
		return "回放"
	case CatAppendAfterSeal:
		return "封存后追加"
	case CatTimeRegression:
		return "时间回退"
	case CatTamper:
		return "篡改"
	case CatContentMismatch:
		return "内容不符"
	case CatCheckpointConflict:
		return "检查点冲突"
	case CatTailTruncation:
		return "尾部截断"
	}
	return "未知"
}

// Report 是校验报告。第一个出错即停止并带出定位信息。
type Report struct {
	Category Category
	// Position 是出错位置（CatOK 与 CatInvalidParam 无意义）。
	Position uint64
	// Verified 是已校验条数。
	Verified uint64
	// Unverifiable 是不可校验条数 u = min(j0, m)。
	Unverifiable uint64
	// EvolveCount 是 evolve 调用次数，恰等于已校验的非换钥条数。
	EvolveCount uint64
	// RekeyCount 是 rekey 调用次数，恰等于已校验的换钥条数。
	RekeyCount uint64
	// Sealed 仅在无错误时有效：最后一条为通过校验的封存记录。
	Sealed bool
}

// Verify 一趟校验条目列表。checkpoints 为 (序号 -> 密钥) 集合，必须非空。
// 位置小于最小检查点序号 j0 的条目不可校验，内容完全不看。
// 每个位置的固定检查次序：检查点冲突、缺口/回放、封存后追加、
// 时间回退、篡改（含非法类型）、内容不符。
func Verify(f Funcs, entries []Entry, checkpoints map[uint64]uint64) Report {
	rep := Report{Category: CatOK}
	if f.Evolve == nil || f.Rekey == nil || f.Mac == nil || len(checkpoints) == 0 {
		rep.Category = CatInvalidParam
		return rep
	}
	m := uint64(len(entries))
	j0 := ^uint64(0)
	for idx := range checkpoints {
		if idx < j0 {
			j0 = idx
		}
	}
	rep.Unverifiable = j0
	if m < j0 {
		rep.Unverifiable = m
	}
	k := checkpoints[j0]
	for t := j0; t < m; t++ {
		if cp, ok := checkpoints[t]; ok && cp != k {
			rep.Category = CatCheckpointConflict
			rep.Position = t
			return rep
		}
		e := entries[t]
		if e.Index != t {
			if e.Index > t {
				rep.Category = CatGap
			} else {
				rep.Category = CatReplay
			}
			rep.Position = t
			return rep
		}
		if t > j0 {
			prev := entries[t-1]
			if prev.Typ == TypSeal {
				rep.Category = CatAppendAfterSeal
				rep.Position = t
				return rep
			}
			if e.Ts < prev.Ts {
				rep.Category = CatTimeRegression
				rep.Position = t
				return rep
			}
		}
		if e.Typ > TypRekey {
			rep.Category = CatTamper
			rep.Position = t
			return rep
		}
		if f.Mac(k, t, e.Typ, e.Ts, e.Data) != e.Tag {
			rep.Category = CatTamper
			rep.Position = t
			return rep
		}
		if e.Typ != TypData && string(e.Data) != strconv.FormatUint(t, 10) {
			rep.Category = CatContentMismatch
			rep.Position = t
			return rep
		}
		rep.Verified++
		if e.Typ == TypRekey {
			k = f.Rekey(k, t)
			rep.RekeyCount++
		} else {
			k = f.Evolve(k)
			rep.EvolveCount++
		}
	}
	if cp, ok := checkpoints[m]; ok && cp != k {
		rep.Category = CatCheckpointConflict
		rep.Position = m
		return rep
	}
	for idx := range checkpoints {
		if idx > m {
			rep.Category = CatTailTruncation
			rep.Position = m
			return rep
		}
	}
	rep.Sealed = rep.Verified > 0 && entries[m-1].Typ == TypSeal
	return rep
}

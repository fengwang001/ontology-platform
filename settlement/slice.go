package settlement

import (
	"math/bits"
	"sort"
)

// slice 是读数区间被切分后的一片：单一版本、单一日、单一时段（或不可计价）。
type slice struct {
	start     int64
	end       int64
	energy    int64
	amount    int64
	key       lineKey
	priceable bool
}

// mulDiv 计算 floor(a*b/q)。本引擎中 a 为电量、b 为片时长、q 为区间总时长
// （b<=q），结果不超过 a，不会溢出。
func mulDiv(a, b, q int64) int64 {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	r, _ := bits.Div64(hi, lo, uint64(q))
	return int64(r)
}

// findSlot 返回覆盖日内秒 x 的时段；调用方保证时段表无缝覆盖整日。
func findSlot(slots []Slot, x int) Slot {
	i := sort.Search(len(slots), func(i int) bool { return slots[i].End > x })
	return slots[i]
}

// sliceInterval 把读数区间 [t0, t1) 内均匀发生的 energy 瓦时切成若干片：
// 切点为落在区间内部的版本切换时刻、日界，以及适用版本时段表的时段边界
// （月界必为日界，天然被切开）。各片电量按时长比例分摊、向下取整，
// 余量归最后一片，使各片之和恰等于区间电量；每片按其所在日类型、时段与
// 版本的单价计价，金额向下取整到分。无版本可用的片记为不可计价。
func sliceInterval(t0, t1, energy int64, vs *versionStore, cal *calendar) []slice {
	cuts := make([]int64, 0, 16)
	cuts = append(cuts, t0)
	cuts = vs.cutsBetween(t0, t1, cuts)
	for d := dayStartOf(t0) + SecondsPerDay; d < t1; d += SecondsPerDay {
		cuts = append(cuts, d)
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i] < cuts[j] })
	bounds := cuts[:0]
	for i, c := range cuts {
		if i == 0 || c != cuts[i-1] {
			bounds = append(bounds, c)
		}
	}
	bounds = append(bounds, t1)

	var out []slice
	for i := 0; i+1 < len(bounds); i++ {
		a, b := bounds[i], bounds[i+1]
		ver := vs.at(a)
		if ver == nil {
			out = append(out, slice{start: a, end: b})
			continue
		}
		dt := cal.dayTypeAt(a)
		base := dayStartOf(a)
		slots := ver.sched[dt]
		inner := make([]int64, 0, 2*len(slots)+2)
		inner = append(inner, a)
		for _, sl := range slots {
			if x := base + int64(sl.Start); x > a && x < b {
				inner = append(inner, x)
			}
			if x := base + int64(sl.End); x > a && x < b {
				inner = append(inner, x)
			}
		}
		inner = append(inner, b)
		sort.Slice(inner, func(i, j int) bool { return inner[i] < inner[j] })
		for j := 0; j+1 < len(inner); j++ {
			sa, sb := inner[j], inner[j+1]
			sl := findSlot(slots, int(sa-base))
			out = append(out, slice{
				start: sa, end: sb, priceable: true,
				key: lineKey{dayType: dt, slotStart: sl.Start, slotEnd: sl.End, price: sl.Price},
			})
		}
	}

	// 电量分摊：按比例向下取整，余量归最后一片。
	d := t1 - t0
	var used int64
	for i := range out {
		if i == len(out)-1 {
			out[i].energy = energy - used
		} else {
			out[i].energy = mulDiv(energy, out[i].end-out[i].start, d)
			used += out[i].energy
		}
	}
	// 计价：金额 = 电量(Wh) * 单价(分/kWh) / 1000，向下取整到分。
	for i := range out {
		if out[i].priceable {
			out[i].amount = out[i].energy * out[i].key.price / whPerKWh
		}
	}
	return out
}

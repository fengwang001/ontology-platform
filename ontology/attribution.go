package ontology

import "sort"

// attributionInput 是一次账单归属所需的全部信息。
type attributionInput struct {
	amount   int64
	startDay int64
	endDay   int64
	method   SplitMethod
}

type dayCandidate struct {
	id      int64
	room    int64
	weight  int64
	checkIn int64
}

// attribute 将一张账单按天归属为各住户（含房东槽位）的整数份额。
// 逐日金额之和恒等于 amount；同一天的余数按入住日早、房间序号小者先得。
func attribute(in attributionInput, residents map[int64]*Resident, roomAreas map[int64]int64) []Contribution {
	totals := map[int64]int64{}
	var landlord int64
	dailyAmount := in.amount / (in.endDay - in.startDay)
	dailyRem := in.amount % (in.endDay - in.startDay)

	for day := in.startDay; day < in.endDay; day++ {
		dayTotal := dailyAmount
		if day-in.startDay < dailyRem { // 前 dailyRem 天各多得 1
			dayTotal++
		}

		cands := dayResidents(day, in.method, residents, roomAreas)
		if len(cands) == 0 {
			if bearer, ok := earliestFutureResident(day, residents); ok {
				totals[bearer] += dayTotal
			} else {
				landlord += dayTotal
			}
			continue
		}

		var weightSum int64
		for _, c := range cands {
			weightSum += c.weight
		}
		base := dayTotal / weightSum
		rem := dayTotal % weightSum
		sortDayCandidates(cands)
		// 余数的每个单位都按同一优先级序列（入住日早、房间小者先得）循环归属：
		// 人头分摊时每权重 1，故每人至多先得 1；面积分摊时高面积住户可连续多得。
		for i, c := range cands {
			share := base * c.weight
			var lo int64
			for j := 0; j < i; j++ {
				lo += cands[j].weight
			}
			hi := lo + c.weight
			up := rem
			if hi < up {
				up = hi
			}
			if up > lo {
				share += up - lo
			}
			totals[c.id] += share
		}
	}

	out := make([]Contribution, 0, len(totals)+1)
	ids := make([]int64, 0, len(totals))
	for id := range totals {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		out = append(out, Contribution{ResidentID: id, Amount: totals[id]})
	}
	if landlord > 0 {
		out = append(out, Contribution{ResidentID: 0, Amount: landlord})
	}
	return out
}

func dayResidents(day int64, method SplitMethod, residents map[int64]*Resident, roomAreas map[int64]int64) []dayCandidate {
	cands := make([]dayCandidate, 0)
	for id, r := range residents {
		room, ok := roomAtDay(r, day)
		if !ok {
			continue
		}
		weight := int64(1)
		if method == SplitByArea {
			weight = roomAreas[room]
		}
		cands = append(cands, dayCandidate{
			id: id, room: room, weight: weight, checkIn: firstCheckIn(r),
		})
	}
	return cands
}

// sortDayCandidates 按入住日早者先得、再按房间序号小者先得排序（余数分配次序）。
func sortDayCandidates(c []dayCandidate) {
	sort.SliceStable(c, func(i, j int) bool {
		if c[i].checkIn != c[j].checkIn {
			return c[i].checkIn < c[j].checkIn
		}
		return c[i].room < c[j].room
	})
}

// earliestFutureResident 找出 day 当天（不含）之后最早入住的住户，
// 同日多人入住取房间序号最小者；从未入住者返回 false（由房东承担）。
func earliestFutureResident(day int64, residents map[int64]*Resident) (int64, bool) {
	type pick struct {
		id   int64
		at   int64
		room int64
	}
	var best *pick
	for id, r := range residents {
		at := firstCheckIn(r)
		if at <= day {
			continue
		}
		room := r.Segments[0].Room
		if best == nil || at < best.at || (at == best.at && room < best.room) {
			best = &pick{id: id, at: at, room: room}
		}
	}
	if best == nil {
		return 0, false
	}
	return best.id, true
}

// landlordContrib 返回归属结果中房东承担的份额。
func landlordContrib(cs []Contribution) int64 {
	for _, c := range cs {
		if c.ResidentID == 0 {
			return c.Amount
		}
	}
	return 0
}

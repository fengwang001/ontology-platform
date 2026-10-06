package staffingtest

import (
	"fmt"
	"math/rand"
	"strings"
)

// GenWorld 描述随机世界。
type GenWorld struct {
	Positions  []string
	Candidates []string
	Bands      map[string][2]int
}

// Generate 生成随机操作序列。时间大体单调（含少量故意回退），
// 通知 ID 用一个与两端实现相同顺序的影子计数记录，供后续操作引用。
func Generate(rng *rand.Rand, n int) (GenWorld, []Op) {
	w := GenWorld{Bands: map[string][2]int{}}
	np := 1 + rng.Intn(4)
	for i := 0; i < np; i++ {
		p := fmt.Sprintf("P%d", i)
		w.Positions = append(w.Positions, p)
		low := 80 + rng.Intn(40)
		w.Bands[p] = [2]int{low, low + rng.Intn(60)}
	}
	nc := 1 + rng.Intn(8)
	for i := 0; i < nc; i++ {
		w.Candidates = append(w.Candidates, fmt.Sprintf("C%d", i))
	}

	var ops []Op
	for _, p := range w.Positions {
		b := w.Bands[p]
		ops = append(ops, Op{
			Kind: "AddPosition", Now: 0, Pos: p,
			BandLow: b[0], BandHigh: b[1], Total: 1 + rng.Intn(4),
		})
	}
	for _, c := range w.Candidates {
		ops = append(ops, Op{Kind: "AddCandidate", Now: 0, Cand: c})
	}
	nextExc := 0
	for qi := 0; qi < 3; qi++ {
		for _, p := range w.Positions {
			if rng.Intn(2) == 0 {
				id := fmt.Sprintf("E%d", nextExc)
				nextExc++
				ops = append(ops, Op{
					Kind: "AddException", Now: 0, ExcID: id, Pos: p,
					Quarter: qi, Total: rng.Intn(3),
				})
			}
		}
	}

	now := 0
	nextID := int64(0)
	var live []int64

	for len(ops) < n {
		switch rng.Intn(4) {
		case 0:
		case 1:
			now += rng.Intn(3)
		case 2:
			now += rng.Intn(10)
		default:
			now += 85 + rng.Intn(15) // 跨越 90 日季度边界
		}
		tryNow := now
		if rng.Intn(12) == 0 {
			tryNow = now - 1 - rng.Intn(5)
		}

		p := w.Positions[rng.Intn(len(w.Positions))]
		band := w.Bands[p]
		c := w.Candidates[rng.Intn(len(w.Candidates))]

		var op Op
		switch rng.Intn(15) {
		case 0, 1, 2:
			salary := band[0] + rng.Intn(band[1]-band[0]+1)
			if rng.Intn(10) < 3 {
				if rng.Intn(2) == 0 {
					salary = band[0] - 1 - rng.Intn(20)
				} else {
					salary = band[1] + 1 + rng.Intn(20)
				}
			}
			op = Op{Kind: "Issue", Now: tryNow, Pos: p, Cand: c,
				Salary: salary, Deadline: tryNow + rng.Intn(12)}
		case 3:
			k := 1 + rng.Intn(3)
			bm := make([]BatchMember, k)
			for i := range bm {
				cc := w.Candidates[rng.Intn(len(w.Candidates))]
				salary := band[0] + rng.Intn(band[1]-band[0]+1)
				if rng.Intn(10) < 3 {
					salary = band[1] + 1 + rng.Intn(20)
				}
				bm[i] = BatchMember{Cand: cc, Salary: salary, Deadline: tryNow + rng.Intn(12)}
			}
			op = Op{Kind: "IssueBatch", Now: tryNow, Pos: p, Batch: bm}
		case 4, 5:
			if len(live) == 0 {
				continue
			}
			id := live[rng.Intn(len(live))]
			op = Op{Kind: "Respond", Now: tryNow, OfferID: id,
				Accept: rng.Intn(2) == 0, Entry: tryNow + rng.Intn(6)}
		case 6:
			if len(live) == 0 {
				continue
			}
			op = Op{Kind: "Onboard", Now: tryNow, OfferID: live[rng.Intn(len(live))]}
		case 7:
			if len(live) == 0 {
				continue
			}
			op = Op{Kind: "Withdraw", Now: tryNow, OfferID: live[rng.Intn(len(live))]}
		case 8:
			if len(live) == 0 {
				continue
			}
			op = Op{Kind: "Cancel", Now: tryNow, OfferID: live[rng.Intn(len(live))]}
		case 9:
			op = Op{Kind: "Leave", Now: tryNow, Cand: c}
		case 10:
			op = Op{Kind: "SetFrozen", Now: tryNow, Pos: p, Frozen: rng.Intn(2) == 0}
		case 11:
			op = Op{Kind: "AdjustHeadcount", Now: tryNow, Pos: p, Total: rng.Intn(6)}
		case 12:
			if len(live) == 0 {
				continue
			}
			op = Op{Kind: "GetOffer", Now: tryNow, OfferID: live[rng.Intn(len(live))]}
		case 13:
			op = Op{Kind: "Occupancy", Now: tryNow, Pos: p}
		default:
			op = Op{Kind: "Snapshot", Now: tryNow}
		}
		ops = append(ops, op)

		// 影子 ID 计数：与真实/模型在成功时分配的序号一致。
		if op.Kind == "Issue" {
			nextID++
			live = append(live, nextID)
		}
		if op.Kind == "IssueBatch" {
			for range op.Batch {
				nextID++
				live = append(live, nextID)
			}
		}
		if tryNow > now {
			now = tryNow
		}
	}
	return w, ops
}

// OpString 生成可读的单步输入描述。
func OpString(op Op) string {
	switch op.Kind {
	case "Issue":
		return fmt.Sprintf("Issue now=%d %s@%s salary=%d deadline=%d",
			op.Now, op.Cand, op.Pos, op.Salary, op.Deadline)
	case "IssueBatch":
		parts := make([]string, len(op.Batch))
		for i, b := range op.Batch {
			parts[i] = fmt.Sprintf("%s:%d/%d", b.Cand, b.Salary, b.Deadline)
		}
		return fmt.Sprintf("IssueBatch now=%d %s [%s]", op.Now, op.Pos, strings.Join(parts, ","))
	case "Respond":
		return fmt.Sprintf("Respond now=%d offer=%d accept=%v entry=%d",
			op.Now, op.OfferID, op.Accept, op.Entry)
	default:
		return fmt.Sprintf("%s %+v", op.Kind, op)
	}
}

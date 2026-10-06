package reins

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// 朴素模型：独立实现，把全部赔款按事故时刻顺序一次性算成，作为对照基准。

type naiveClaim struct {
	no, policyNo, eventID string
	time, amount          int64
}

type naivePolicy struct {
	sumInsured int64
	ces        Cession
}

func naiveAllocate(tr Treaty, pols map[string]naivePolicy, claims []naiveClaim) map[string]Split {
	nets := make([]int64, len(claims))
	splits := make(map[string]Split, len(claims))
	type evAgg struct {
		id      string
		time    int64
		net     int64
		members []int
	}
	events := map[string]*evAgg{}
	for i, c := range claims {
		p := pols[c.policyNo]
		qs := c.amount * p.ces.QuotaShare / p.sumInsured
		sur := c.amount * p.ces.Surplus / p.sumInsured
		nets[i] = c.amount - qs - sur
		splits[c.no] = Split{QuotaShare: qs, Surplus: sur, FinalNet: nets[i]}
		ev, ok := events[c.eventID]
		if !ok {
			ev = &evAgg{id: c.eventID, time: c.time}
			events[c.eventID] = ev
		}
		if c.time < ev.time {
			ev.time = c.time
		}
		ev.net += nets[i]
		ev.members = append(ev.members, i)
	}
	order := make([]*evAgg, 0, len(events))
	for _, ev := range events {
		order = append(order, ev)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].time != order[j].time {
			return order[i].time < order[j].time
		}
		return order[i].id < order[j].id
	})
	remaining := tr.XLLimit * int64(tr.Reinstatements+1)
	for _, ev := range order {
		var paid int64
		if ev.net > tr.XLRetention {
			paid = ev.net - tr.XLRetention
			if paid > tr.XLLimit {
				paid = tr.XLLimit
			}
			if paid > remaining {
				paid = remaining
			}
		}
		remaining -= paid
		idxs := append([]int(nil), ev.members...)
		sort.Slice(idxs, func(a, b int) bool { return claims[idxs[a]].no < claims[idxs[b]].no })
		share := make([]int64, len(claims))
		var assigned int64
		for _, i := range idxs {
			share[i] = nets[i] * paid / ev.net
			assigned += share[i]
		}
		for k := int64(0); k < paid-assigned; k++ {
			share[idxs[k]]++
		}
		for _, i := range idxs {
			s := splits[claims[i].no]
			s.XLRecover = share[i]
			s.FinalNet -= share[i]
			splits[claims[i].no] = s
		}
	}
	return splits
}

// 随机到达次序 + 随机撤销的赔款序列，与按事故时刻一次算成的朴素模型对照；
// 日志打印输入、输出与判定依据。
func TestRandomOrderMatchesNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	tr := Treaty{
		QuotaSharePercent: 1 + rng.Intn(99),
		SurplusRetention:  int64(rng.Intn(5000)),
		SurplusLines:      1 + rng.Intn(4),
		XLRetention:       int64(rng.Intn(3000)),
		XLLimit:           int64(1 + rng.Intn(5000)),
		Reinstatements:    rng.Intn(4),
	}
	t.Logf("输入-合约条款: %+v", tr)
	e := mustEngine(t, tr)
	pols := map[string]naivePolicy{}
	for i := 0; i < 8; i++ {
		no := fmt.Sprintf("P%d", i)
		s := int64(100 + rng.Intn(20000))
		ces, err := e.RegisterPolicy(no, s, 0, 365)
		if err != nil {
			t.Fatalf("RegisterPolicy(%s): %v", no, err)
		}
		pols[no] = naivePolicy{sumInsured: s, ces: ces}
		t.Logf("输入-登记保单 %s 保额 %d -> 分出结构 %+v", no, s, ces)
	}
	var claims []naiveClaim
	for i := 0; i < 150; i++ {
		pno := fmt.Sprintf("P%d", rng.Intn(8))
		claims = append(claims, naiveClaim{
			no:       fmt.Sprintf("C%d", i),
			policyNo: pno,
			eventID:  fmt.Sprintf("E%d", rng.Intn(15)),
			time:     int64(rng.Intn(365 * 86400)),
			amount:   1 + int64(rng.Intn(int(pols[pno].sumInsured))),
		})
	}
	order := rng.Perm(len(claims))
	live := map[string]bool{}
	for _, i := range order {
		c := claims[i]
		if _, err := e.AddClaim(c.no, c.policyNo, c.eventID, c.time, c.amount); err != nil {
			t.Fatalf("AddClaim(%+v): %v", c, err)
		}
		live[c.no] = true
		t.Logf("输入-录入赔款 %+v", c)
	}
	removed := 0
	for _, i := range order {
		if removed >= 40 || rng.Intn(2) == 0 {
			continue
		}
		if err := e.RemoveClaim(claims[i].no); err != nil {
			t.Fatalf("RemoveClaim(%s): %v", claims[i].no, err)
		}
		delete(live, claims[i].no)
		removed++
		t.Logf("输入-撤销赔款 %s", claims[i].no)
	}
	var final []naiveClaim
	for _, c := range claims {
		if live[c.no] {
			final = append(final, c)
		}
	}
	want := naiveAllocate(tr, pols, final)
	// 对照组：同一赔款集合按事故时刻顺序一次性录入新引擎。
	sorted := append([]naiveClaim(nil), final...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].time != sorted[j].time {
			return sorted[i].time < sorted[j].time
		}
		return sorted[i].no < sorted[j].no
	})
	e2 := mustEngine(t, tr)
	for no, p := range pols {
		mustRegister(t, e2, no, p.sumInsured, 0, 365)
	}
	for _, c := range sorted {
		mustAdd(t, e2, c.no, c.policyNo, c.eventID, c.time, c.amount)
	}
	bad := 0
	for _, c := range final {
		got, err := e.ClaimResult(c.no)
		if err != nil {
			t.Fatalf("ClaimResult(%s): %v", c.no, err)
		}
		w := want[c.no]
		g2, err := e2.ClaimResult(c.no)
		if err != nil {
			t.Fatalf("ClaimResult(%s): %v", c.no, err)
		}
		if got != w || g2 != w {
			t.Errorf("判定失败 %s: 乱序引擎 %+v / 时序引擎 %+v != 朴素模型 %+v (输入 %+v)",
				c.no, got, g2, w, c)
			bad++
		}
	}
	t.Logf("判定依据: 存活赔款 %d 笔、撤销 %d 笔，逐笔对比乱序引擎/时序引擎/朴素模型三方归属，不一致 %d 笔",
		len(final), removed, bad)
	if bad > 0 {
		t.Fatalf("共 %d 笔不一致", bad)
	}
}

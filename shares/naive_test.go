package shares_test

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	"ontology/shares"
)

// naiveModel 是按规格逐条直译的朴素参照实现：有效权重与最大余数
// 均用 big.Int 计算，余数分配用反复扫描取最大者的朴素方式，
// 与被测实现相互独立。
type naiveModel struct {
	wSlow  int64
	total  int64
	capPer int64
	maxNow int64
	hosts  map[string]*naiveHost
}

type naiveHost struct {
	weight  int64
	join    int64
	healthy bool
}

func newNaive(wSlow, total, capPer int64) *naiveModel {
	return &naiveModel{
		wSlow:  wSlow,
		total:  total,
		capPer: capPer,
		hosts:  make(map[string]*naiveHost),
	}
}

func (m *naiveModel) checkTime(now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return shares.ErrInvalidTime
	}
	if now < m.maxNow {
		return shares.ErrClockRollback
	}
	return nil
}

func (m *naiveModel) accept(now int64) {
	if now > m.maxNow {
		m.maxNow = now
	}
}

func (m *naiveModel) addHost(id string, weight int64, now int64) error {
	if id == "" || weight < 1 || weight > 1_000_000 {
		return shares.ErrInvalidParam
	}
	if err := m.checkTime(now); err != nil {
		return err
	}
	if _, ok := m.hosts[id]; ok {
		return shares.ErrHostExists
	}
	m.accept(now)
	m.hosts[id] = &naiveHost{weight: weight, join: now, healthy: true}
	return nil
}

func (m *naiveModel) setWeight(id string, weight int64, now int64) error {
	if id == "" || weight < 1 || weight > 1_000_000 {
		return shares.ErrInvalidParam
	}
	if err := m.checkTime(now); err != nil {
		return err
	}
	h, ok := m.hosts[id]
	if !ok {
		return shares.ErrHostNotFound
	}
	m.accept(now)
	h.weight = weight
	return nil
}

func (m *naiveModel) setHealth(id string, healthy bool, now int64) error {
	if id == "" {
		return shares.ErrInvalidParam
	}
	if err := m.checkTime(now); err != nil {
		return err
	}
	h, ok := m.hosts[id]
	if !ok {
		return shares.ErrHostNotFound
	}
	m.accept(now)
	if healthy && !h.healthy {
		h.join = now
	}
	h.healthy = healthy
	return nil
}

func (m *naiveModel) removeHost(id string, now int64) error {
	if id == "" {
		return shares.ErrInvalidParam
	}
	if err := m.checkTime(now); err != nil {
		return err
	}
	if _, ok := m.hosts[id]; !ok {
		return shares.ErrHostNotFound
	}
	m.accept(now)
	delete(m.hosts, id)
	return nil
}

// effective 用 big.Int 计算有效权重，避免任何溢出假设。
func (m *naiveModel) effective(h *naiveHost, now int64) int64 {
	if !h.healthy {
		return 0
	}
	e := now - h.join
	if e >= m.wSlow {
		return h.weight
	}
	num := new(big.Int).Mul(big.NewInt(h.weight), big.NewInt(e))
	num.Div(num, big.NewInt(m.wSlow))
	if num.Sign() < 1 {
		return 1
	}
	return num.Int64()
}

// shares 按规格分轮计算，trace 返回判定依据（有效权重、每轮底数、
// 余数、差额归属与固定过程），用于日志打印。
func (m *naiveModel) shares(now int64) ([]shares.Share, string, error) {
	if err := m.checkTime(now); err != nil {
		return nil, "", err
	}
	m.accept(now)

	ids := make([]string, 0, len(m.hosts))
	for id := range m.hosts {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var trace strings.Builder
	eff := make(map[string]int64, len(ids))
	active := make([]string, 0, len(ids))
	for _, id := range ids {
		eff[id] = m.effective(m.hosts[id], now)
		if m.hosts[id].healthy {
			active = append(active, id)
		}
	}
	fmt.Fprintf(&trace, "eff=%v", eff)
	if len(active) == 0 {
		return nil, trace.String(), shares.ErrNoHealthyHosts
	}

	final := make(map[string]int64, len(active))
	tRem := m.total
	for round := 1; len(active) > 0; round++ {
		sumEff := int64(0)
		for _, id := range active {
			sumEff += eff[id]
		}
		bigE := big.NewInt(sumEff)
		base := make(map[string]int64, len(active))
		rem := make(map[string]int64, len(active))
		baseSum := int64(0)
		for _, id := range active {
			p := new(big.Int).Mul(big.NewInt(eff[id]), big.NewInt(tRem))
			q, r := new(big.Int).QuoRem(p, bigE, new(big.Int))
			base[id] = q.Int64()
			rem[id] = r.Int64()
			baseSum += base[id]
		}
		// 朴素分配差额：反复扫描 (余数最大, id 最小) 者，每实例至多加一份。
		leftover := tRem - baseSum
		used := make(map[string]bool, len(active))
		var bonus []string
		for k := int64(0); k < leftover; k++ {
			best := ""
			for _, id := range active {
				if used[id] {
					continue
				}
				if best == "" || rem[id] > rem[best] ||
					(rem[id] == rem[best] && id < best) {
					best = id
				}
			}
			used[best] = true
			base[best]++
			bonus = append(bonus, best)
		}
		fmt.Fprintf(&trace, " | round%d: E=%d base=%v rem=%v bonus=%v",
			round, sumEff, base, rem, bonus)

		var capped []string
		for _, id := range active {
			if base[id] > m.capPer {
				capped = append(capped, id)
			}
		}
		if len(capped) == 0 {
			for _, id := range active {
				final[id] = base[id]
			}
			tRem = 0
			break
		}
		sort.Strings(capped)
		fmt.Fprintf(&trace, " capped=%v", capped)
		next := make([]string, 0, len(active)-len(capped))
		for _, id := range active {
			isCapped := false
			for _, c := range capped {
				if id == c {
					isCapped = true
					break
				}
			}
			if isCapped {
				final[id] = m.capPer
				tRem -= m.capPer
			} else {
				next = append(next, id)
			}
		}
		active = next
	}
	if tRem > 0 {
		fmt.Fprintf(&trace, " | 容量不足: Trem=%d", tRem)
		return nil, trace.String(), shares.ErrInsufficientCapacity
	}

	out := make([]shares.Share, 0, len(ids))
	for _, id := range ids {
		out = append(out, shares.Share{ID: id, Value: final[id]})
	}
	return out, trace.String(), nil
}

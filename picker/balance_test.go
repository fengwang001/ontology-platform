package picker

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// TestBalance 固定种子统计：二选一的负载分布比"取首个候选"更均衡。
//
// 判定依据：两种策略消费完全相同的 (r1, r2) 随机流与相同的请求到达过程，
// 负载以在途数衡量（P2C 的经典指标）：比较各端点时间在途均值的标准差
// 与全程最大在途数，P2C 必须严格更小。
func TestBalance(t *testing.T) {
	const (
		seed  = 20261005
		nEp   = 8
		steps = 20000
		tau   = int64(1000)
		p0    = int64(50)
		pf    = int64(5000)
		m     = 100000
		nmax  = 16
	)
	ids := make([]string, nEp)
	for k := range ids {
		ids[k] = string(rune('a' + k))
	}
	type stats struct {
		counts   map[string]int     // 各端点被选次数
		meanLoad map[string]float64 // 各端点时间在途均值
		maxLoad  int                // 全程单端点最大在途
	}
	run := func(p2c bool) stats {
		rng := rand.New(rand.NewSource(seed))
		s := newSim(tau, p0, pf, m, nmax, p2c)
		for _, id := range ids {
			if err := s.add(id); err != nil {
				t.Fatalf("add %q: %v", id, err)
			}
		}
		st := stats{counts: make(map[string]int, nEp), meanLoad: make(map[string]float64, nEp)}
		type pending struct {
			due    int64
			ticket uint64
			rtt    int64
		}
		var queue []pending
		var now int64
		for step := 0; step < steps; step++ {
			now++
			keep := queue[:0]
			for _, p := range queue {
				if p.due <= now {
					if err := s.release(p.ticket, p.rtt, true, now); err != nil {
						t.Fatalf("release: %v", err)
					}
				} else {
					keep = append(keep, p)
				}
			}
			queue = keep
			thisRTT := 10 + rng.Int63n(81) // 每请求时延 10~90ms，避免常量时延造成系统性并列
			tk, id, err := s.pick(now, rng.Uint64(), rng.Uint64())
			if err != nil {
				t.Fatalf("pick: %v", err)
			}
			st.counts[id]++
			queue = append(queue, pending{due: now + thisRTT, ticket: tk, rtt: thisRTT})
			for _, eid := range ids {
				load := s.eps[eid].inflight
				st.meanLoad[eid] += float64(load) / steps
				if load > st.maxLoad {
					st.maxLoad = load
				}
			}
		}
		return st
	}
	stddev := func(loads map[string]float64) (std, minC, maxC float64) {
		var sum, sq float64
		minC = math.MaxFloat64
		for _, id := range ids {
			c := loads[id]
			sum += c
			sq += c * c
			if c < minC {
				minC = c
			}
			if c > maxC {
				maxC = c
			}
		}
		mean := sum / float64(len(ids))
		return math.Sqrt(sq/float64(len(ids)) - mean*mean), minC, maxC
	}
	format := func(loads map[string]float64) string {
		sorted := append([]string(nil), ids...)
		sort.Strings(sorted)
		parts := make([]string, 0, len(sorted))
		for _, id := range sorted {
			parts = append(parts, fmt.Sprintf("%s=%.2f", id, loads[id]))
		}
		return strings.Join(parts, " ")
	}
	p2c := run(true)
	base := run(false)
	p2cStd, p2cMin, p2cMax := stddev(p2c.meanLoad)
	baseStd, baseMin, baseMax := stddev(base.meanLoad)
	t.Logf("输入: seed=%d 端点=%d 步数=%d rtt~U[10,90]ms 两策略共享同一 (r1,r2) 随机流与请求时延序列", seed, nEp, steps)
	t.Logf("输出 P2C      : 在途均值std=%.3f min=%.2f max=%.2f 峰值在途=%d 分布: %s",
		p2cStd, p2cMin, p2cMax, p2c.maxLoad, format(p2c.meanLoad))
	t.Logf("输出 取首个候选: 在途均值std=%.3f min=%.2f max=%.2f 峰值在途=%d 分布: %s",
		baseStd, baseMin, baseMax, base.maxLoad, format(base.meanLoad))
	t.Logf("判定依据: P2C 的端点间在途均值标准差(%.3f)与峰值在途(%d) 均应严格小于取首个候选(%.3f, %d)",
		p2cStd, p2c.maxLoad, baseStd, base.maxLoad)
	if p2cStd >= baseStd {
		t.Fatalf("P2C 在途未更均衡: std %.3f >= %.3f", p2cStd, baseStd)
	}
	if p2c.maxLoad >= base.maxLoad {
		t.Fatalf("P2C 峰值在途未更低: %d >= %d", p2c.maxLoad, base.maxLoad)
	}
}

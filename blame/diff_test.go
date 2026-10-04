package blame

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/dag"
)

// naiveModel 是完全独立的朴素参考实现：每次评估都从第 0 期全量重扫，
// 不做任何增量优化，用来对照增量实现的告警序列与冻结根因。
type naiveModel struct {
	T      int64
	ds     map[string]naiveDS
	order  []string
	now    int64
	lands  map[string]map[int64]int64
	frozen map[string]map[int64]Root
}

type naiveDS struct {
	off, dur int64
	parents  []string
}

func newNaive(T int64) *naiveModel {
	return &naiveModel{
		T:      T,
		ds:     make(map[string]naiveDS),
		lands:  make(map[string]map[int64]int64),
		frozen: make(map[string]map[int64]Root),
	}
}

func (m *naiveModel) add(name string, off, dur int64, parents []string) bool {
	if _, ok := m.ds[name]; ok {
		return false
	}
	ps := append([]string(nil), parents...)
	sort.Strings(ps)
	m.ds[name] = naiveDS{off: off, dur: dur, parents: ps}
	m.order = append(m.order, name)
	m.lands[name] = make(map[int64]int64)
	m.frozen[name] = make(map[int64]Root)
	return true
}

func (m *naiveModel) deadline(name string, k int64) int64 {
	return k*m.T + m.ds[name].off
}

func (m *naiveModel) violating(name string, k, t int64) bool {
	if lt, ok := m.lands[name][k]; ok {
		return lt > m.deadline(name, k)
	}
	return t > m.deadline(name, k)
}

// canLand 复刻 Land 前置条件；返回错误供两侧错误类别对照。
func (m *naiveModel) canLand(name string, k, now int64) error {
	if now < m.now {
		return dag.ErrClockRewind
	}
	d, ok := m.ds[name]
	if !ok {
		return dag.ErrNoDataset
	}
	next := int64(len(m.lands[name]))
	if k < next {
		return dag.ErrAlready
	}
	if k > next {
		return dag.ErrOutOfOrder
	}
	if k > 0 && now < k*m.T {
		return dag.ErrTooEarly
	}
	for _, p := range d.parents {
		if _, ok := m.lands[p][k]; !ok {
			return fmt.Errorf("%w: %s", dag.ErrUpstreamMissing, p)
		}
	}
	return nil
}

func (m *naiveModel) land(name string, k, now int64) bool {
	if err := m.canLand(name, k, now); err != nil {
		return false
	}
	m.lands[name][k] = now
	m.now = now
	return true
}

func (m *naiveModel) trace(name string, k, t int64) Root {
	x := name
	for {
		d := m.ds[x]
		if len(d.parents) == 0 {
			return Root{Dataset: x, Kind: Self}
		}
		var ready int64
		finite := true
		var critical string
		for _, p := range d.parents {
			if pt, ok := m.lands[p][k]; ok {
				if pt > ready {
					ready = pt
				}
			} else {
				finite = false
				if critical == "" {
					critical = p
				}
			}
		}
		if finite {
			if ready+d.dur <= m.deadline(x, k) {
				return Root{Dataset: x, Kind: Self}
			}
			for _, p := range d.parents {
				if m.lands[p][k] == ready {
					critical = p
					break
				}
			}
		}
		if !m.violating(critical, k, t) {
			return Root{Dataset: x, Kind: Unreachable}
		}
		x = critical
	}
}

func (m *naiveModel) evaluate(now int64) []Alert {
	m.now = now
	type gk struct {
		root Root
		k    int64
	}
	groups := make(map[gk][]string)
	keys := make([]gk, 0)
	for _, name := range m.order {
		upper := now/m.T + 2
		if int64(len(m.lands[name]))+1 > upper {
			upper = int64(len(m.lands[name])) + 1
		}
		for k := int64(0); k <= upper; k++ {
			if r, ok := m.frozen[name][k]; ok && r.Dataset != "" {
				continue
			}
			if !m.violating(name, k, now) {
				continue
			}
			root := m.trace(name, k, now)
			m.frozen[name][k] = root
			key := gk{root: root, k: k}
			if _, exists := groups[key]; !exists {
				keys = append(keys, key)
			}
			groups[key] = append(groups[key], name)
		}
	}
	var out []Alert
	for _, key := range keys {
		list := append([]string(nil), groups[key]...)
		sort.Strings(list)
		out = append(out, Alert{K: key.k, Root: key.root, Affected: list})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].K != out[j].K {
			return out[i].K < out[j].K
		}
		if out[i].Root.Dataset != out[j].Root.Dataset {
			return out[i].Root.Dataset < out[j].Root.Dataset
		}
		return out[i].Root.Kind < out[j].Root.Kind
	})
	return out
}

func (m *naiveModel) blame(name string, k int64) (Root, bool) {
	r, ok := m.frozen[name][k]
	if !ok || r.Dataset == "" {
		return Root{}, false
	}
	return r, true
}

func serializeAlerts(al []Alert) string {
	var parts []string
	for _, a := range al {
		parts = append(parts, fmt.Sprintf("k=%d|%s|%s|[%s]",
			a.K, a.Root.Dataset, a.Root.Kind, strings.Join(a.Affected, ",")))
	}
	return strings.Join(parts, ";")
}

func sameErrClass(a, b error) bool {
	for _, target := range []error{
		dag.ErrClockRewind, dag.ErrNoDataset, dag.ErrAlready,
		dag.ErrOutOfOrder, dag.ErrTooEarly, dag.ErrUpstreamMissing,
	} {
		if errors.Is(a, target) != errors.Is(b, target) {
			return false
		}
	}
	return true
}

// TestRandomVsNaive 用 1500 组随机图与操作序列对照增量实现与朴素全量重扫模型。
// 失败时打印输入操作、两侧告警与判定依据（Blame 冻结根因）。
func TestRandomVsNaive(t *testing.T) {
	const cases = 1500
	for seed := int64(0); seed < cases; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			T := int64(2 + rng.Intn(50))
			real, err := New(T)
			if err != nil {
				t.Fatal(err)
			}
			naive := newNaive(T)

			var logb strings.Builder
			fmt.Fprintf(&logb, "T=%d\n", T)

			nDS := 2 + rng.Intn(6)
			names := make([]string, 0, nDS)
			for i := 0; i < nDS; i++ {
				name := fmt.Sprintf("d%02d", i)
				off := int64(1 + rng.Intn(int(T)))
				dur := int64(rng.Intn(int(T) + 1))
				var parents []string
				if len(names) > 0 && rng.Intn(2) == 0 {
					pc := 1 + rng.Intn(2)
					if pc > len(names) {
						pc = len(names)
					}
					for _, pi := range rng.Perm(len(names))[:pc] {
						parents = append(parents, names[pi])
					}
				}
				if real.AddDataset(name, off, dur, parents) == nil &&
					naive.add(name, off, dur, parents) {
					names = append(names, name)
					fmt.Fprintf(&logb, "ADD %s off=%d dur=%d parents=%v\n",
						name, off, dur, parents)
				}
			}

			ops := 6 + rng.Intn(20)
			for op := 0; op < ops; op++ {
				if rng.Intn(2) == 0 {
					name := names[rng.Intn(len(names))]
					next := int64(len(naive.lands[name]))
					var k int64
					switch {
					case next > 0 && rng.Intn(6) == 0:
						k = next - 1 - int64(rng.Intn(int(next))) // 重复落地
					case rng.Intn(6) == 0:
						k = next + 1 + int64(rng.Intn(3)) // 跳期
					default:
						k = next
					}
					now := naive.now
					switch rng.Intn(5) {
					case 0:
						now = naive.now - 1 - rng.Int63n(3) // 回退
					case 1:
						now = k*T - 1 // 过早
					default:
						if k*T > now {
							now = k * T
						}
						now += rng.Int63n(3 * T)
					}
					if now < 0 {
						now = 0
					}
					fmt.Fprintf(&logb, "LAND %s k=%d now=%d\n", name, k, now)

					realErr := real.Land(name, k, now)
					naiveErr := naive.canLand(name, k, now)
					naiveOK := naive.land(name, k, now)
					if (realErr == nil) != naiveOK {
						t.Fatalf("input:\n%saccept mismatch real=%v naiveErr=%v",
							logb.String(), realErr, naiveErr)
					}
					if (realErr == nil) != (naiveErr == nil) ||
						(realErr != nil && !sameErrClass(realErr, naiveErr)) {
						t.Fatalf("input:\n%serror mismatch real=%v naive=%v",
							logb.String(), realErr, naiveErr)
					}
				} else {
					now := naive.now
					if rng.Intn(5) == 0 {
						now = naive.now - 1 - rng.Int63n(3)
					} else {
						now += rng.Int63n(2 * T)
					}
					if now < 0 {
						now = 0
					}
					fmt.Fprintf(&logb, "EVAL now=%d\n", now)

					realAlerts, realErr := real.Evaluate(now)
					naiveOK := now >= naive.now
					var naiveAlerts []Alert
					if naiveOK {
						naiveAlerts = naive.evaluate(now)
					}
					if (realErr == nil) != naiveOK {
						t.Fatalf("input:\n%seval accept real=%v naiveOK=%v",
							logb.String(), realErr, naiveOK)
					}
					if realErr == nil {
						rs, ns := serializeAlerts(realAlerts), serializeAlerts(naiveAlerts)
						if rs != ns {
							t.Fatalf("input:\n%sALERT MISMATCH\nreal : %s\nnaive: %s",
								logb.String(), rs, ns)
						}
					}
				}
			}

			// 终局评估：把所有潜在违约都暴露出来。
			finalNow := naive.now + T
			fmt.Fprintf(&logb, "EVAL now=%d (final)\n", finalNow)
			realAlerts, err := real.Evaluate(finalNow)
			if err != nil {
				t.Fatalf("final eval: %v\ninput:\n%s", err, logb.String())
			}
			naiveAlerts := naive.evaluate(finalNow)
			if rs, ns := serializeAlerts(realAlerts), serializeAlerts(naiveAlerts); rs != ns {
				t.Fatalf("input:\n%sFINAL ALERT MISMATCH\nreal : %s\nnaive: %s",
					logb.String(), rs, ns)
			}

			// 逐项核对冻结根因（判定依据）。
			upper := finalNow/T + 2
			for _, name := range names {
				for k := int64(0); k <= upper; k++ {
					got, gerr := real.Blame(name, k)
					want, wok := naive.blame(name, k)
					if (gerr == nil) != wok {
						t.Fatalf("input:\n%sBlame(%s,%d) existence real=%v naive=%v",
							logb.String(), name, k, gerr, wok)
					}
					if wok && (got != want) {
						t.Fatalf("input:\n%sBlame(%s,%d) real=%s/%s naive=%s/%s",
							logb.String(), name, k,
							got.Dataset, got.Kind, want.Dataset, want.Kind)
					}
				}
			}
		})
	}
}

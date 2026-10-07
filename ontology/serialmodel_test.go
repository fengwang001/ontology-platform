package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// naiveModel 是一个独立实现的朴素全局串行模型：单把全局锁、逐条判定，
// 不共享被测 Store 的任何代码路径（除类型定义外）。它消费同一批判定日志，
// 对每一条决策独立给出“在全局串行执行下应得的结果”，并维护最终状态。
type naiveModel struct {
	versions map[string]int64
	states   map[string]string
	props    map[string]Props
	holders  map[string]string
	expire   map[string]int64
	staged   map[string]map[string]staged // leaseToken(调用方) -> key -> staged
}

type staged struct {
	state string
	props Props
}

func newNaiveModel(keys []string) *naiveModel {
	m := &naiveModel{
		versions: map[string]int64{},
		states:   map[string]string{},
		props:    map[string]Props{},
		holders:  map[string]string{},
		expire:   map[string]int64{},
		staged:   map[string]map[string]staged{},
	}
	for _, k := range keys {
		m.versions[k] = 0 // create 决策会把它置为 1
	}
	return m
}

func sortedKeys(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func (m *naiveModel) holderAt(key string, at int64) string {
	h, ok := m.holders[key]
	if !ok || h == "" {
		return ""
	}
	if m.expire[key] <= at {
		return ""
	}
	return h
}

// decide 返回该决策在朴素全局串行顺序（日志 Seq）下应得的结果。
func (m *naiveModel) decide(d Decision) Outcome {
	switch d.Kind {
	case "create":
		m.versions[d.Keys[0]] = 1
		m.states[d.Keys[0]] = d.State
		m.props[d.Keys[0]] = cloneProps(d.Patch)
		return OutcomeCommitted

	case "acquire":
		keys := sortedKeys(d.Keys)
		for _, k := range keys {
			if _, ok := m.versions[k]; !ok {
				return OutcomeVersionStale
			}
			if m.holderAt(k, d.At) != "" {
				if len(keys) > 1 {
					return OutcomeLockConflict
				}
				return OutcomeOccupied
			}
		}
		m.staged[d.Caller+"#"+fmt.Sprint(d.Token)] = map[string]staged{}
		for _, k := range keys {
			m.holders[k] = d.Caller
			m.expire[k] = d.At + d.TTL
		}
		return OutcomeCommitted

	case "update":
		k := d.Keys[0]
		if _, ok := m.versions[k]; !ok {
			return OutcomeVersionStale
		}
		if h := m.holderAt(k, d.At); h != "" {
			return OutcomeOptimisticRejected
		}
		if m.versions[k] != d.BaseVer {
			return OutcomeVersionStale
		}
		if m.props[k] == nil {
			m.props[k] = Props{}
		}
		for pk, pv := range d.Patch {
			m.props[k][pk] = pv
		}
		m.versions[k]++
		return OutcomeCommitted

	case "commit", "release":
		keys := sortedKeys(d.Keys)
		for _, k := range keys {
			if m.holderAt(k, d.At) != d.Caller {
				return OutcomeInvalidLease
			}
		}
		st := m.staged[d.Caller+"#"+fmt.Sprint(d.Token)]
		for _, k := range keys {
			if d.Kind == "commit" {
				if sd, ok := st[k]; ok {
					m.states[k] = sd.state
					m.props[k] = cloneProps(sd.props)
				}
				m.versions[k]++
			}
			m.holders[k] = ""
			m.expire[k] = 0
		}
		return OutcomeCommitted

	case "stage":
		k := d.Keys[0]
		if m.holderAt(k, d.At) != d.Caller {
			return OutcomeInvalidLease
		}
		st := m.staged[d.Caller+"#"+fmt.Sprint(d.Token)]
		st[k] = staged{state: d.State, props: cloneProps(d.Patch)}
		return OutcomeCommitted

	case "heartbeat":
		for _, k := range d.Keys {
			if m.holderAt(k, d.At) != d.Caller {
				return OutcomeInvalidLease
			}
		}
		for _, k := range d.Keys {
			m.expire[k] = d.At + d.TTL
		}
		return OutcomeCommitted

	case "lease_expired":
		// 惰性过期事件：朴素模型在下次触碰时同样会观察到，结果只是标记。
		return OutcomeInvalidLease

	case "link":
		// 随机测试不产生 link 决策；基数在专门用例中覆盖。
		return d.Outcome
	}
	return d.Outcome
}

// 随机生成并发操作序列：占用动作（占用→暂存→提交/释放）与普通乐观更新
// 在多个 goroutine 中交错发起；随后以判定日志的 Seq 作为唯一串行顺序，
// 用朴素模型重放，逐条比对结果与最终状态。
func TestRandomizedAgainstNaiveSerial(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		clk := &FakeClock{T: 1}
		s := NewStore(clk)

		keys := []string{"k0", "k1", "k2", "k3"}
		for _, k := range keys {
			s.CreateInstance(k, "open", Props{"v": "0"})
		}

		var wg sync.WaitGroup
		barrier := make(chan struct{})
		const actors = 8
		for a := 0; a < actors; a++ {
			wg.Add(1)
			go func(a int) {
				defer wg.Done()
				<-barrier
				localRNG := rand.New(rand.NewSource(seed*1000 + int64(a)))
				caller := fmt.Sprintf("g%d", a)
				for step := 0; step < 12; step++ {
					if localRNG.Intn(2) == 0 {
						// 普通乐观更新：先读后改，版本号来自调用方自己的观察时刻。
						snap, _ := s.Snapshot(keys[localRNG.Intn(len(keys))])
						s.Update(caller, snap.Key, snap.Version,
							Patch{"v": fmt.Sprintf("%s-%d-%d", caller, step, localRNG.Intn(1000))})
						continue
					}
					// 动作：申请 1~2 个实例的独占占用。
					ks := []string{keys[localRNG.Intn(len(keys))]}
					if localRNG.Intn(2) == 0 {
						ks = append(ks, keys[localRNG.Intn(len(keys))])
					}
					l, out := s.TryAcquire(caller, ks, 1_000_000)
					if out != OutcomeCommitted {
						continue // 被拒绝方立即收到结果，不重试（重试由后续迭代自然产生新判定）
					}
					target := ks[0]
					l.Stage(target, "busy", Patch{"v": caller + "-held"}, nil)
					if localRNG.Intn(4) == 0 {
						l.Release()
					} else {
						l.Stage(target, "done", Patch{"v": caller + "-done"}, nil)
						l.Commit()
					}
				}
			}(a)
		}
		close(barrier)
		wg.Wait()

		// 所有租约此时都已终结（commit/release），不存在悬挂占用。
		for _, k := range keys {
			snap, _ := s.Snapshot(k)
			if snap.Occupied {
				t.Fatalf("seed %d: dangling occupancy on %s", seed, k)
			}
		}

		// 以日志顺序（即系统给出的唯一可推导顺序）驱动朴素模型重放。
		model := newNaiveModel(keys)
		for _, d := range s.Log().Entries() {
			want := model.decide(d)
			if want != d.Outcome {
				t.Fatalf("seed %d seq %d (%s/%s keys=%v): model=%v actual=%v reason=%q",
					seed, d.Seq, d.Kind, d.Caller, d.Keys, want, d.Outcome, d.Reason)
			}
		}

		// 最终版本与属性必须与朴素串行模型完全一致。
		for _, k := range keys {
			snap, _ := s.Snapshot(k)
			if snap.Version != model.versions[k] {
				t.Fatalf("seed %d key %s version: model=%d actual=%d",
					seed, k, model.versions[k], snap.Version)
			}
			if snap.Props["v"] != model.props[k]["v"] || snap.State != model.states[k] {
				t.Fatalf("seed %d key %s state mismatch: model=(%s,%s) actual=(%s,%s)",
					seed, k, model.states[k], model.props[k]["v"], snap.State, snap.Props["v"])
			}
		}

		// 串行化不变量：任何成功提交的普通 update，都不得线性化在任一存活
		// 占用区间内部（同键的 acquire 与 commit/release 之间）。
		assertNoUpdateInsideOccupancy(t, s.Log().Entries())
	}
}

func assertNoUpdateInsideOccupancy(t *testing.T, ds []Decision) {
	t.Helper()
	type span struct{ begin, end int64 }
	spans := map[string][]span{}
	open := map[string]int64{} // key -> acquire seq
	for _, d := range ds {
		switch d.Kind {
		case "acquire":
			if d.Outcome == OutcomeCommitted {
				for _, k := range d.Keys {
					open[k] = d.Seq
				}
			}
		case "commit", "release":
			if d.Outcome == OutcomeCommitted {
				for _, k := range d.Keys {
					if b, ok := open[k]; ok {
						spans[k] = append(spans[k], span{b, d.Seq})
						delete(open, k)
					}
				}
			}
		case "update":
			if d.Outcome != OutcomeCommitted {
				continue
			}
			k := d.Keys[0]
			for _, sp := range spans[k] {
				if d.Seq > sp.begin && d.Seq < sp.end {
					t.Fatalf("committed update seq %d inside occupancy span [%d,%d] on %s",
						d.Seq, sp.begin, sp.end, k)
				}
			}
		}
	}
}

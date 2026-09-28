package broadcast

import (
	"bytes"
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// 朴素参照：不做任何投递穿插，按固定顺序发布所有规则、发送所有数据后，
// 再把每个实例一次性投递到最新版本。每条数据只按其到达时的全局版本判定。
type naiveEngine struct {
	n        int
	versions map[int64]RuleSet
	data     []struct {
		key, value int
		tag        int64
		seq        int64
	}
	seq int64
}

func newNaive(n int) *naiveEngine {
	return &naiveEngine{n: n, versions: map[int64]RuleSet{}}
}

func (m *naiveEngine) publish(rules RuleSet) int64 {
	version := int64(len(m.versions)) + 1
	snapshot := make(RuleSet, len(rules))
	copy(snapshot, rules)
	m.versions[version] = snapshot
	return version
}

func (m *naiveEngine) send(key, value int) {
	m.seq++
	m.data = append(m.data, struct {
		key, value int
		tag        int64
		seq        int64
	}{key, value, int64(len(m.versions)), m.seq})
}

func (m *naiveEngine) hits() []Hit {
	byInstance := make([][]Hit, m.n)
	for _, d := range m.data {
		target := d.key % m.n
		var matched []Rule
		for _, rule := range m.versions[d.tag] {
			if d.value >= rule.Threshold {
				matched = append(matched, rule)
			}
		}
		for i := 0; i < len(matched); i++ {
			for j := i + 1; j < len(matched); j++ {
				if matched[j].ID < matched[i].ID {
					matched[i], matched[j] = matched[j], matched[i]
				}
			}
		}
		for _, rule := range matched {
			byInstance[target] = append(byInstance[target], Hit{
				Instance: target,
				Key:      d.key,
				Value:    d.value,
				RuleID:   rule.ID,
				Version:  d.tag,
				Seq:      d.seq,
			})
		}
	}
	var out []Hit
	for _, group := range byInstance {
		out = append(out, group...)
	}
	return out
}

func hitsByInstance(hits []Hit, n int) [][]Hit {
	groups := make([][]Hit, n)
	for _, hit := range hits {
		groups[hit.Instance] = append(groups[hit.Instance], hit)
	}
	return groups
}

// hitKey 去掉全局到达序号 Seq：同一批数据由多个执行体并发发送时，
// 其到达先后由调度器决定，不属于可复现性承诺；其余字段必须完全一致。
type hitKey struct {
	Instance   int
	Key, Value int
	RuleID     string
	Version    int64
}

func hitMultiset(groups [][]Hit) []hitKey {
	var keys []hitKey
	for _, group := range groups {
		for _, hit := range group {
			keys = append(keys, hitKey{hit.Instance, hit.Key, hit.Value, hit.RuleID, hit.Version})
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Instance != b.Instance {
			return a.Instance < b.Instance
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.Value != b.Value {
			return a.Value < b.Value
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.RuleID < b.RuleID
	})
	return keys
}

// assertOrderingInvariants 校验：同实例内按到达序号单调，
// 且同一条数据（同一 Seq）的多条命中按规则 ID 升序。
func assertOrderingInvariants(t *testing.T, groups [][]Hit) {
	t.Helper()
	for inst, group := range groups {
		for i, hit := range group {
			if i > 0 {
				prev := group[i-1]
				if hit.Seq < prev.Seq {
					t.Fatalf("instance %d seq not monotonic: %d after %d", inst, hit.Seq, prev.Seq)
				}
				if hit.Seq == prev.Seq && hit.RuleID < prev.RuleID {
					t.Fatalf("instance %d rules not sorted: %s before %s", inst, hit.RuleID, prev.RuleID)
				}
			}
		}
	}
}

func mustPublish(t *testing.T, eng *Engine, rules RuleSet) int64 {
	t.Helper()
	version, err := eng.Publish(rules)
	if err != nil {
		t.Fatalf("publish %v: %v", rules, err)
	}
	return version
}

func TestVersionJumpAndBufferedFlush(t *testing.T) {
	var log bytes.Buffer
	eng := NewEngine(2, 8, WithLogger(&log))

	mustPublish(t, eng, RuleSet{{ID: "a", Threshold: 10}})
	mustPublish(t, eng, RuleSet{{ID: "a", Threshold: 5}, {ID: "b", Threshold: 7}})

	// 两个实例版本均为 0：key=3 路由到实例 1，标签 2，进入缓冲。
	if hits, err := eng.Send(3, 8); err != nil || hits != nil {
		t.Fatalf("buffered send = %v, %v", hits, err)
	}
	if v, _ := eng.InstanceVersion(1); v != 0 {
		t.Fatalf("instance version = %d, want 0", v)
	}

	// 实例 1 跨越投递版本 1（没有标签为 1 的数据），再投递版本 2 时刷出缓冲。
	if err := eng.Deliver(1, 1); err != nil {
		t.Fatal(err)
	}
	if err := eng.Deliver(1, 2); err != nil {
		t.Fatal(err)
	}

	hits := eng.Hits()
	if len(hits) != 2 || hits[0].RuleID != "a" || hits[1].RuleID != "b" {
		t.Fatalf("flushed hits = %+v", hits)
	}
	if hits[0].Version != 2 || hits[0].Instance != 1 || hits[0].Key != 3 {
		t.Fatalf("hit metadata wrong: %+v", hits[0])
	}

	if !strings.Contains(log.String(), "SEND buffered") ||
		!strings.Contains(log.String(), "DELIVER ok instance=1 applied=2") {
		t.Fatalf("log missing buffer/deliver evidence:\n%s", log.String())
	}
	t.Logf("step log:\n%s", log.String())
}

func TestImmediateThenBufferOrderByArrival(t *testing.T) {
	eng := NewEngine(1, 8)
	mustPublish(t, eng, RuleSet{{ID: "a", Threshold: 1}})
	if err := eng.Deliver(0, 1); err != nil {
		t.Fatal(err)
	}

	// 版本一致：立即处理。
	hits, err := eng.Send(0, 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("immediate send = %v, %v", hits, err)
	}

	// 发布新版本但尚未投递：后续数据进入缓冲，刷出时按新版本判定。
	mustPublish(t, eng, RuleSet{{ID: "a", Threshold: 100}})
	if _, err := eng.Send(1, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Send(2, 50); err != nil {
		t.Fatal(err)
	}
	if err := eng.Deliver(0, 2); err != nil {
		t.Fatal(err)
	}

	all := eng.Hits()
	if len(all) != 1 {
		t.Fatalf("buffered data must be judged by v2 threshold, got %+v", all)
	}
	if all[0].Key != 0 || all[0].Version != 1 {
		t.Fatalf("unexpected surviving hit: %+v", all[0])
	}
}

func TestRuleDeletion(t *testing.T) {
	eng := NewEngine(1, 4)
	mustPublish(t, eng, RuleSet{{ID: "a", Threshold: 1}, {ID: "b", Threshold: 1}})
	mustPublish(t, eng, RuleSet{{ID: "a", Threshold: 1}}) // 删除规则 b
	if err := eng.Deliver(0, 1); err != nil {
		t.Fatal(err)
	}
	if err := eng.Deliver(0, 2); err != nil {
		t.Fatal(err)
	}

	hits, err := eng.Send(0, 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].RuleID != "a" {
		t.Fatalf("rule b should be deleted, got %+v", hits)
	}
}

func TestInvalidInputsLeaveNoTrace(t *testing.T) {
	eng := NewEngine(2, 1)
	mustPublish(t, eng, RuleSet{{ID: "a", Threshold: 1}})

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"empty rule id", func() error { _, err := eng.Publish(RuleSet{{ID: "", Threshold: 1}}); return err }, ErrInvalidRule},
		{"duplicate rule id", func() error {
			_, err := eng.Publish(RuleSet{{ID: "x", Threshold: 1}, {ID: "x", Threshold: 2}})
			return err
		}, ErrInvalidRule},
		{"deliver negative instance", func() error { return eng.Deliver(-1, 1) }, ErrInstanceOutOfRange},
		{"deliver instance too large", func() error { return eng.Deliver(2, 1) }, ErrInstanceOutOfRange},
		{"deliver skip version", func() error { return eng.Deliver(0, 2) }, ErrVersionOutOfRange},
		{"deliver unpublished version", func() error {
			if err := eng.Deliver(0, 1); err != nil {
				t.Fatal(err)
			}
			return eng.Deliver(0, 2)
		}, ErrVersionOutOfRange},
		{"negative key", func() error { _, err := eng.Send(-1, 0); return err }, ErrNegativeKey},
	}

	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	// 上面的用例已把实例 0 投递到版本 1。发布版本 2 后第一条数据占满缓冲，
	// 第二条（同样路由到实例 0）必须以 ErrBufferFull 被整体拒绝。
	mustPublish(t, eng, RuleSet{{ID: "a", Threshold: 1}})
	if _, err := eng.Send(0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Send(2, 1); !errors.Is(err, ErrBufferFull) {
		t.Fatalf("buffer full err = %v", err)
	}

	if got := eng.GlobalVersion(); got != 2 {
		t.Fatalf("global version = %d, want 2", got)
	}
	if v, _ := eng.InstanceVersion(0); v != 1 {
		t.Fatalf("instance 0 version = %d, want 1", v)
	}
	if v, _ := eng.InstanceVersion(1); v != 0 {
		t.Fatalf("instance 1 version = %d, want 0", v)
	}

	// 刷出后缓冲中只能有第一条数据；阈值 1 使其产生一条命中。
	if err := eng.Deliver(0, 2); err != nil {
		t.Fatal(err)
	}
	hits := eng.Hits()
	if len(hits) != 1 || hits[0].Key != 0 {
		t.Fatalf("rejected send left a trace, hits = %+v", hits)
	}

	// 所有错误哨兵互不相同、可用 errors.Is 区分。
	sentinels := []error{ErrInvalidRule, ErrInstanceOutOfRange, ErrNegativeKey, ErrBufferFull, ErrVersionOutOfRange}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("error sentinels not distinguishable: %v vs %v", sentinels[i], sentinels[j])
			}
		}
	}
}

func TestMatchesNaiveReferenceAcrossDeliverSchedules(t *testing.T) {
	const instances = 3
	const bufferCap = 64

	ruleVersions := []RuleSet{
		{{ID: "r1", Threshold: 10}, {ID: "r2", Threshold: 20}},
		{{ID: "r1", Threshold: 15}},
		{{ID: "r2", Threshold: 5}, {ID: "r3", Threshold: 50}},
		{},
		{{ID: "r1", Threshold: 0}},
	}

	type datum struct{ key, value int }
	rng := rand.New(rand.NewSource(305))
	data := make([]datum, 0, 60)
	for i := 0; i < 60; i++ {
		data = append(data, datum{key: rng.Intn(30), value: rng.Intn(60)})
	}

	// 发布与发送交错成固定序列；朴素模型只依赖这条序列，与投递无关。
	naive := newNaive(instances)
	order := rng.Perm(len(data))
	publishAt := map[int]int{5: 0, 15: 1, 25: 2, 40: 3, 55: 4}
	for i := 0; i < len(data); i++ {
		if idx, ok := publishAt[i]; ok {
			naive.publish(ruleVersions[idx])
		}
		d := data[order[i]]
		naive.send(d.key, d.value)
	}

	for _, schedule := range []string{"eager", "batch", "random"} {
		t.Run(schedule, func(t *testing.T) {
			eng := NewEngine(instances, bufferCap)
			scheduleRNG := rand.New(rand.NewSource(7001))
			for i := 0; i < len(data); i++ {
				if idx, ok := publishAt[i]; ok {
					mustPublish(t, eng, ruleVersions[idx])
				}
				d := data[order[i]]
				if _, err := eng.Send(d.key, d.value); err != nil {
					t.Fatal(err)
				}

				global := eng.GlobalVersion()
				switch schedule {
				case "eager":
					for inst := 0; inst < instances; inst++ {
						v, _ := eng.InstanceVersion(inst)
						for v < global {
							v++
							if err := eng.Deliver(inst, v); err != nil {
								t.Fatal(err)
							}
						}
					}
				case "batch":
					// 只追赶实例 0，其余实例保持落后、缓冲数据。
					v, _ := eng.InstanceVersion(0)
					for v < global {
						v++
						if err := eng.Deliver(0, v); err != nil {
							t.Fatal(err)
						}
					}
				case "random":
					inst := scheduleRNG.Intn(instances)
					if v, _ := eng.InstanceVersion(inst); v < global && scheduleRNG.Intn(2) == 0 {
						if err := eng.Deliver(inst, v+1); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			for inst := 0; inst < instances; inst++ {
				v, _ := eng.InstanceVersion(inst)
				for v < int64(len(ruleVersions)) {
					v++
					if err := eng.Deliver(inst, v); err != nil {
						t.Fatal(err)
					}
				}
			}

			got := hitsByInstance(eng.Hits(), instances)
			want := hitsByInstance(naive.hits(), instances)
			for inst := range got {
				if len(got[inst]) != len(want[inst]) {
					t.Fatalf("instance %d: %d hits, want %d\ngot=%v\nwant=%v",
						inst, len(got[inst]), len(want[inst]), got[inst], want[inst])
				}
				for k := range got[inst] {
					if got[inst][k] != want[inst][k] {
						t.Fatalf("instance %d hit %d = %+v, want %+v",
							inst, k, got[inst][k], want[inst][k])
					}
				}
			}
		})
	}
}

func TestConcurrentPublishSendDeliver(t *testing.T) {
	const instances = 4
	const bufferCap = 256
	eng := NewEngine(instances, bufferCap)

	// 固定的发布与发送交错序列：发布 v1 -> 发送批次 0 -> 发布 v2 ->
	// 发送批次 1 -> 发布 v3 -> 发送批次 2。发送由多个执行体并发完成，
	// 版本投递则由独立执行体在整个过程中并发穿插；引擎串行化全部状态变更，
	// 最终结果必须与“按同一序列发布发送、最后统一投递”的朴素参照一致。
	ruleVersions := []RuleSet{
		{{ID: "a", Threshold: 3}, {ID: "b", Threshold: 8}},
		{{ID: "a", Threshold: 6}},
		{{ID: "b", Threshold: 1}, {ID: "c", Threshold: 9}},
	}

	type datum struct{ key, value int }
	var data []datum
	for key := 0; key < 24; key++ {
		for value := 0; value < 10; value++ {
			data = append(data, datum{key, value})
		}
	}
	chunks := [][]datum{
		data[:len(data)/2],
		data[len(data)/2 : 3*len(data)/4],
		data[3*len(data)/4:],
	}

	naive := newNaive(instances)
	for i, rules := range ruleVersions {
		naive.publish(rules)
		for _, d := range chunks[i] {
			naive.send(d.key, d.value)
		}
	}

	stop := make(chan struct{})
	var deliverers sync.WaitGroup
	for inst := 0; inst < instances; inst++ {
		inst := inst
		deliverers.Add(1)
		go func() {
			defer deliverers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				v, _ := eng.InstanceVersion(inst)
				if v >= int64(len(ruleVersions)) {
					return
				}
				// 越序/未发布投递只会被拒绝；成功时恰好推进一个版本。
				_ = eng.Deliver(inst, v+1)
			}
		}()
	}

	for phase, rules := range ruleVersions {
		mustPublish(t, eng, rules)
		var senders sync.WaitGroup
		const workers = 4
		for w := 0; w < workers; w++ {
			worker := w
			senders.Add(1)
			go func() {
				defer senders.Done()
				for idx, d := range chunks[phase] {
					if idx%workers == worker {
						if _, err := eng.Send(d.key, d.value); err != nil {
							t.Errorf("send (%d,%d): %v", d.key, d.value, err)
						}
					}
				}
			}()
		}
		senders.Wait()
	}
	close(stop)
	deliverers.Wait()

	// 兜底：确保所有实例最终追平。
	for inst := 0; inst < instances; inst++ {
		v, _ := eng.InstanceVersion(inst)
		for v < int64(len(ruleVersions)) {
			v++
			if err := eng.Deliver(inst, v); err != nil {
				t.Fatal(err)
			}
		}
	}

	got := hitsByInstance(eng.Hits(), instances)
	want := hitsByInstance(naive.hits(), instances)
	assertOrderingInvariants(t, got)
	if gotKeys, wantKeys := hitMultiset(got), hitMultiset(want); !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("hit multiset mismatch:\ngot =%v\nwant=%v", gotKeys, wantKeys)
	}
}

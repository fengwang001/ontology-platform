package ontology

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, replicas int) *Reclaimer {
	t.Helper()
	r, err := NewReclaimer(replicas)
	if err != nil {
		t.Fatalf("NewReclaimer(%d) failed: %v", replicas, err)
	}
	return r
}

func mustApply(t *testing.T, r *Reclaimer, events ...Event) {
	t.Helper()
	t.Logf("输入事件: %+v", events)
	if err := r.Apply(events); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	t.Logf("应用后视图: %+v", r.View())
}

// TestLexicographicOverride 字典序覆盖：大者覆盖旧值，相等或更小者被忽略。
func TestLexicographicOverride(t *testing.T) {
	r := mustNew(t, 2)

	mustApply(t, r, Event{Replica: 0, Key: "k", Vector: Vector{1, 0}, Value: "v1"})
	if got := r.View().Entries["k"].Value; got != "v1" {
		t.Fatalf("got %q, want v1", got)
	}

	// [0,9] 字典序小于 [1,0]（首分量 0 < 1），属于旧事件，必须被忽略。
	mustApply(t, r, Event{Replica: 1, Key: "k", Vector: Vector{0, 9}, Value: "stale"})
	if got := r.View().Entries["k"].Value; got != "v1" {
		t.Fatalf("stale event overwrote winner: got %q, want v1", got)
	}
	t.Logf("判定依据: [0,9] < [1,0]（字典序，首分量 0<1），旧事件被忽略，赢家仍为 v1")

	// 重复事件 [1,0] 与赢家相等，同样被忽略。
	mustApply(t, r, Event{Replica: 0, Key: "k", Vector: Vector{1, 0}, Value: "dup"})
	if got := r.View().Entries["k"].Value; got != "v1" {
		t.Fatalf("duplicate event overwrote winner: got %q, want v1", got)
	}
	t.Logf("判定依据: [1,0] == [1,0]，重复事件被忽略，赢家仍为 v1")

	// [1,1] 字典序大于 [1,0]，覆盖旧值。
	mustApply(t, r, Event{Replica: 1, Key: "k", Vector: Vector{1, 1}, Value: "v2"})
	if got := r.View().Entries["k"].Value; got != "v2" {
		t.Fatalf("got %q, want v2", got)
	}
	t.Logf("判定依据: [1,1] > [1,0]（首分量相等，次分量 1>0），新事件覆盖，赢家为 v2")

	// 被忽略事件的向量仍并入副本时钟：副本 1 见过 [0,9] 与 [1,1]。
	wantClock1 := Vector{1, 9}
	if got := r.View().Clocks[1]; !reflect.DeepEqual(got, wantClock1) {
		t.Fatalf("clock[1] = %v, want %v", got, wantClock1)
	}
	t.Logf("判定依据: 被忽略事件不改变存储，但副本时钟照常取最大，clock[1]=%v", wantClock1)
}

// TestStableVectorMonotonic 稳定向量随事件推进单调不减。
func TestStableVectorMonotonic(t *testing.T) {
	r := mustNew(t, 3)
	prev := r.StableVector()
	t.Logf("初始稳定向量: %v", prev)

	batches := [][]Event{
		{{Replica: 0, Key: "a", Vector: Vector{2, 0, 0}, Value: "x"}},
		{{Replica: 1, Key: "b", Vector: Vector{2, 3, 0}, Value: "y"}},
		{{Replica: 2, Key: "c", Vector: Vector{2, 3, 4}, Value: "z"}},
		{{Replica: 0, Key: "d", Vector: Vector{5, 3, 4}, Value: "w"}},
	}
	for i, batch := range batches {
		mustApply(t, r, batch...)
		got := r.StableVector()
		t.Logf("第 %d 批后稳定向量: %v", i+1, got)
		if !LessEqual(prev, got) {
			t.Fatalf("stable vector regressed: %v -> %v", prev, got)
		}
		t.Logf("判定依据: 稳定向量为各副本时钟逐分量最小值，时钟只增，故 %v <= %v 单调成立", prev, got)
		prev = got
	}
	// 各副本时钟为 [5,3,4]、[2,3,0]、[2,3,4]，逐分量最小值为 [2,3,0]。
	if want := (Vector{2, 3, 0}); !reflect.DeepEqual(prev, want) {
		t.Fatalf("final stable = %v, want %v", prev, want)
	}
}

// TestReclaimOnlyStableTombstones 回收只收向量逐分量不超过稳定向量的墓碑。
func TestReclaimOnlyStableTombstones(t *testing.T) {
	r := mustNew(t, 2)
	mustApply(t, r,
		Event{Replica: 0, Key: "dead", Vector: Vector{1, 0}, Tombstone: true},
		Event{Replica: 0, Key: "live", Vector: Vector{2, 0}, Value: "v"},
		Event{Replica: 0, Key: "recent-dead", Vector: Vector{3, 0}, Tombstone: true},
	)
	// 副本 1 只追平到 [2,0]，稳定向量为 [2,0]。
	mustApply(t, r, Event{Replica: 1, Key: "other", Vector: Vector{2, 0}, Value: "o"})

	stable := r.StableVector()
	t.Logf("稳定向量: %v", stable)

	removed := r.Reclaim()
	t.Logf("回收结果: %v", removed)
	if want := []string{"dead"}; !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
	view := r.View()
	if _, ok := view.Entries["live"]; !ok {
		t.Fatal("non-tombstone entry 'live' must not be reclaimed")
	}
	if _, ok := view.Entries["recent-dead"]; !ok {
		t.Fatal("tombstone 'recent-dead' vector [3,0] > stable [2,0], must not be reclaimed")
	}
	t.Logf("判定依据: dead[1,0]<=%v 且为墓碑故回收；live 非墓碑保留；recent-dead[3,0]>%v 保留", stable, stable)

	// 推进副本 1 时钟后，recent-dead 也可回收。
	mustApply(t, r, Event{Replica: 1, Key: "other2", Vector: Vector{3, 1}, Value: "o2"})
	removed = r.Reclaim()
	t.Logf("第二次回收结果: %v", removed)
	if want := []string{"recent-dead"}; !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
	if want := []string{"dead", "recent-dead"}; !reflect.DeepEqual(r.View().Reclaimed, want) {
		t.Fatalf("reclaimed = %v, want %v", r.View().Reclaimed, want)
	}
	t.Logf("判定依据: 稳定向量推进到 [3,1] 后 recent-dead[3,0]<=[3,1]，被回收并记入已回收列表")
}

// TestStaleEventDoesNotReviveDeletedKey 旧事件不复活已删键。
func TestStaleEventDoesNotReviveDeletedKey(t *testing.T) {
	r := mustNew(t, 2)
	mustApply(t, r, Event{Replica: 0, Key: "k", Vector: Vector{3, 0}, Tombstone: true})

	// 迟到的写入 [1,0] 字典序小于墓碑向量 [3,0]，不得复活该键。
	mustApply(t, r, Event{Replica: 1, Key: "k", Vector: Vector{1, 0}, Value: "ghost"})
	entry, ok := r.View().Entries["k"]
	if !ok || !entry.Tombstone {
		t.Fatalf("stale write revived deleted key: %+v", entry)
	}
	t.Logf("判定依据: 迟到写 [1,0] < 墓碑 [3,0]，被忽略，键保持墓碑状态")

	// 回收后键被移除，重复的旧写同样不得复活。
	mustApply(t, r, Event{Replica: 1, Key: "x", Vector: Vector{3, 1}, Value: "x"})
	if removed := r.Reclaim(); !reflect.DeepEqual(removed, []string{"k"}) {
		t.Fatalf("removed = %v, want [k]", removed)
	}
	mustApply(t, r, Event{Replica: 1, Key: "k", Vector: Vector{1, 0}, Value: "ghost"})
	if _, ok := r.View().Entries["k"]; ok {
		t.Fatal("stale write resurrected reclaimed key")
	}
	t.Logf("判定依据: 回收后旧写 [1,0] 不大于已回收墓碑向量，键不复活")
}

// TestInvalidInputRejected 四类非法输入被拒且状态不变；批内任一非法则整批不生效。
func TestInvalidInputRejected(t *testing.T) {
	if _, err := NewReclaimer(0); !errors.Is(err, ErrNonPositiveReplicas) {
		t.Fatalf("NewReclaimer(0) err = %v, want ErrNonPositiveReplicas", err)
	}
	t.Logf("输入: 副本数 0 -> 拒绝: %v", ErrNonPositiveReplicas)

	r := mustNew(t, 2)
	mustApply(t, r, Event{Replica: 0, Key: "k", Vector: Vector{1, 0}, Value: "v"})
	before := r.View()

	cases := []struct {
		name string
		ev   Event
		want error
	}{
		{"副本编号越界", Event{Replica: 2, Key: "a", Vector: Vector{1, 1}}, ErrReplicaOutOfRange},
		{"向量含负分量", Event{Replica: 0, Key: "a", Vector: Vector{1, -1}}, ErrNegativeComponent},
		{"键为空", Event{Replica: 0, Key: "", Vector: Vector{1, 1}}, ErrEmptyKey},
		{"向量长度不符", Event{Replica: 0, Key: "a", Vector: Vector{1}}, ErrVectorLengthMismatch},
	}
	for _, tc := range cases {
		err := r.Apply([]Event{tc.ev})
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		t.Logf("输入: %+v -> 拒绝: %v（判定依据: %s）", tc.ev, err, tc.name)
	}

	// 批内任一非法则整批不生效：第一条合法也不得应用。
	err := r.Apply([]Event{
		{Replica: 1, Key: "good", Vector: Vector{1, 5}, Value: "g"},
		{Replica: 9, Key: "bad", Vector: Vector{1, 1}},
	})
	if !errors.Is(err, ErrReplicaOutOfRange) {
		t.Fatalf("batch err = %v, want ErrReplicaOutOfRange", err)
	}
	t.Logf("输入: 合法+非法混合批 -> 整批拒绝: %v", err)

	after := r.View()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state changed after rejected batches:\nbefore=%+v\nafter=%+v", before, after)
	}
	t.Logf("判定依据: 失败后存储、时钟、稳定向量与已回收列表逐字段不变，DeepEqual 通过")
}

// TestConcurrentReadOnlyConsistency 并发只读同一实例得到的视图逐字段相同。
func TestConcurrentReadOnlyConsistency(t *testing.T) {
	r := mustNew(t, 3)
	mustApply(t, r,
		Event{Replica: 0, Key: "a", Vector: Vector{2, 0, 0}, Value: "va"},
		Event{Replica: 1, Key: "b", Vector: Vector{2, 2, 0}, Tombstone: true},
		Event{Replica: 2, Key: "c", Vector: Vector{2, 2, 2}, Value: "vc"},
	)
	r.Reclaim()
	want := r.View()
	t.Logf("基准视图: %+v", want)

	const readers = 32
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				got := r.View()
				if !reflect.DeepEqual(got, want) {
					errs <- errors.New("view mismatch")
					return
				}
				if stable := r.StableVector(); !reflect.DeepEqual(stable, want.Stable) {
					errs <- errors.New("stable vector mismatch")
					return
				}
				if err := r.SelfCheck(); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent read: %v", err)
	}
	t.Logf("判定依据: %d 个并发读者各读 50 次，视图/稳定向量/自检结果与基准逐字段相同", readers)
}

// TestSelfCheck 自检在合法状态下通过。
func TestSelfCheck(t *testing.T) {
	r := mustNew(t, 2)
	mustApply(t, r,
		Event{Replica: 0, Key: "a", Vector: Vector{1, 0}, Value: "v"},
		Event{Replica: 1, Key: "a", Vector: Vector{1, 1}, Tombstone: true},
	)
	if err := r.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck failed: %v", err)
	}
	t.Logf("判定依据: 时钟非负、稳定向量不超过各时钟、条目向量不超过时钟上界、已回收键不在存储中")
}

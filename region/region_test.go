package region

import (
	"errors"
	"testing"
)

func TestCurrentWinsAndGetErrors(t *testing.T) {
	r := New("A")

	if _, err := r.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("空键应不存在, got %v", err)
	}

	v1 := r.Put("k", 1, 5)
	v2 := r.Put("k", 2, 10)
	if v1.ID.Seq != 1 || v2.ID.Seq != 2 {
		t.Fatalf("本地序号应从1递增, got %d %d", v1.ID.Seq, v2.ID.Seq)
	}
	if cur, _ := r.Current("k"); cur.ID != v2.ID {
		t.Fatalf("ts 大者应为当前")
	}

	// ts 相等：origin 字节序决胜（与到达先后无关）。
	r.Apply(Version{ID: VersionID{Origin: "B", Seq: 9}, Key: "k", TS: 10})
	if cur, _ := r.Current("k"); cur.ID.Origin != "B" {
		t.Fatalf("ts 相等时 origin 字节序大者胜, got %s", cur.ID.Origin)
	}
	r.Apply(Version{ID: VersionID{Origin: "C", Seq: 1}, Key: "k", TS: 10})
	if cur, _ := r.Current("k"); cur.ID.Origin != "C" {
		t.Fatalf("origin C 应胜过 B")
	}
	// ts、origin 都相同：本地序号大者胜。
	r.Apply(Version{ID: VersionID{Origin: "C", Seq: 7}, Key: "k", TS: 10})
	if cur, _ := r.Current("k"); cur.ID.Seq != 7 {
		t.Fatalf("同 ts/origin 序号大者胜, got %d", cur.ID.Seq)
	}

	// 先到高版本，再后补低版本，当前不变。
	r.Apply(Version{ID: VersionID{Origin: "A", Seq: 100}, Key: "late", TS: 100})
	r.Apply(Version{ID: VersionID{Origin: "A", Seq: 101}, Key: "late", TS: 1})
	if cur, _ := r.Current("late"); cur.ID.Seq != 100 {
		t.Fatalf("当前版本不应由到达次序决定")
	}

	// 标记为当前：Get 区分“被标记删除”。
	m := r.Delete("k", 20)
	if !m.Marker || m.Size != 0 {
		t.Fatalf("删除标记大小应为0")
	}
	if _, err := r.Get("k"); !errors.Is(err, ErrDeleted) {
		t.Fatalf("标记当前应报被标记删除, got %v", err)
	}
	// 更早的标记不影响；无标记的键仍报不存在。
	if _, err := r.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("无版本键应报不存在, got %v", err)
	}
}

func TestApplyIdempotentAndProbes(t *testing.T) {
	r := New("A")
	v := Version{ID: VersionID{Origin: "B", Seq: 3}, Key: "k", Size: 4, TS: 9}
	if dup := r.Apply(v); dup {
		t.Fatalf("首次 Apply 不应重复")
	}
	if got := r.Versions(); len(got) != 1 || !got[0].Replica {
		t.Fatalf("Apply 的版本应标记为副本")
	}
	if !r.Has(v.ID) {
		t.Fatalf("Has 应命中已应用标识")
	}
	for i := 0; i < 5; i++ {
		if dup := r.Apply(v); !dup {
			t.Fatalf("同标识再次 Apply 必须计重复且不改状态")
		}
	}
	if got := r.Versions(); len(got) != 1 {
		t.Fatalf("重复 Apply 不得新增版本, got %d", len(got))
	}
	if r.Duplicates() != 5 {
		t.Fatalf("重复计数应为5, got %d", r.Duplicates())
	}
	// 每次 Apply 恰好 1 次记录探测（含命中与未命中），与版本总数无关。
	if r.ApplyProbes() != 6 {
		t.Fatalf("探测次数应为6, got %d", r.ApplyProbes())
	}
}

func TestApplyProbesIndependentOfVersionCount(t *testing.T) {
	for _, n := range []int{100, 10000} {
		r := New("A")
		for i := 1; i <= n; i++ {
			r.Put("bulk", 1, int64(i))
		}
		before := r.ApplyProbes()
		probe := Version{ID: VersionID{Origin: "Z", Seq: 1}, Key: "x", TS: 1}
		r.Apply(probe)
		r.Apply(probe)
		after := r.ApplyProbes()
		if after-before != 2 {
			t.Fatalf("n=%d 时两次 Apply 只应触碰2条记录, got %d", n, after-before)
		}
	}
}

func TestOtherKeyUnaffected(t *testing.T) {
	r := New("A")
	a := r.Put("a", 1, 1)
	r.Put("b", 1, 99)
	if cur, ok := r.Current("a"); !ok || cur.ID != a.ID {
		t.Fatalf("不同 key 的版本互不影响")
	}
}

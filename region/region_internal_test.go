package region

import "testing"

// TestApplyProbeConstant 证明 Apply 的重复判定触碰记录数恒为 1，
// 与区域内版本总数（100 与 10000 两档）无关。
func TestApplyProbeConstant(t *testing.T) {
	for _, n := range []int{100, 10000} {
		g := New("R")
		// 先在区域内制造 n 个不同键的版本（直接写存储，模拟本地创建）。
		for i := 1; i <= n; i++ {
			v := Version{
				ID:   VersionID{Origin: "R", Seq: int64(i)},
				Key:  keyName(i),
				Size: 1,
				TS:   1,
			}
			g.putLocked(v)
		}
		g.seq = int64(n)

		// 对一个已存在标识做 50 次重复 Apply：每次只允许触碰 1 条记录。
		const repeats = 50
		before := g.ApplyProbes()
		target := Version{
			ID:      VersionID{Origin: "R", Seq: int64(n / 2)},
			Key:     keyName(n / 2),
			Size:    1,
			TS:      1,
			Replica: false,
		}
		dups := 0
		for i := 0; i < repeats; i++ {
			if g.Apply(target) {
				dups++
			}
		}
		got := g.ApplyProbes() - before
		if dups != repeats {
			t.Fatalf("n=%d: dups=%d, want %d", n, dups, repeats)
		}
		if got != repeats {
			t.Fatalf("n=%d: probe touches=%d, want exactly %d (1 per Apply)", n, got, repeats)
		}
		if len(g.versions) != n {
			t.Fatalf("n=%d: duplicate Apply changed storage: %d versions", n, len(g.versions))
		}

		// 一次新 Apply（不存在）同样只触碰 1 条。
		before = g.ApplyProbes()
		fresh := Version{
			ID:   VersionID{Origin: "S", Seq: 1},
			Key:  "foreign",
			Size: 1,
			TS:   1,
		}
		if g.Apply(fresh) {
			t.Fatalf("n=%d: fresh version reported duplicate", n)
		}
		if g.ApplyProbes()-before != 1 {
			t.Fatalf("n=%d: fresh Apply probes=%d, want 1", n, g.ApplyProbes()-before)
		}
	}
}

func keyName(i int) string {
	// 避免 strconv 依赖；10000 以内用定长字节键即可保持唯一。
	return string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)) + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [12]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}

func TestCurrentTieBreakOrder(t *testing.T) {
	g := New("A")
	older := Version{ID: VersionID{Origin: "A", Seq: 1}, Key: "k", TS: 5}
	sameTS := Version{ID: VersionID{Origin: "B", Seq: 9}, Key: "k", TS: 5}
	newerTS := Version{ID: VersionID{Origin: "A", Seq: 2}, Key: "k", TS: 6}
	g.Apply(older)
	g.Apply(sameTS)
	g.Apply(newerTS)
	cur, ok := g.Current("k")
	if !ok || cur.ID != newerTS.ID {
		t.Fatalf("current=%v ok=%v, want newer ts", cur.ID, ok)
	}
	// 删除最大 ts 版本不会发生（版本不可删）；改为新区域更大 origin 决胜：
	g2 := New("A")
	g2.Apply(older)
	g2.Apply(sameTS)
	cur2, _ := g2.Current("k")
	if cur2.ID.Origin != "B" {
		t.Fatalf("same ts tie: origin=%s, want B", cur2.ID.Origin)
	}
	// 同 origin 同 ts，seq 大的胜。
	g3 := New("A")
	s1 := Version{ID: VersionID{Origin: "A", Seq: 1}, Key: "k", TS: 5}
	s2 := Version{ID: VersionID{Origin: "A", Seq: 2}, Key: "k", TS: 5}
	g3.Apply(s2)
	g3.Apply(s1) // 逆序到达
	cur3, _ := g3.Current("k")
	if cur3.ID.Seq != 2 {
		t.Fatalf("same origin/ts: seq=%d, want 2 (arrival-independent)", cur3.ID.Seq)
	}
}

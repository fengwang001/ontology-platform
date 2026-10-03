package notify

import (
	"strconv"
	"sync"
	"testing"

	"ontology/alertstore"
	"ontology/suppress"
)

// 静默半开边界 [start,end)：t=start 生效，t=end 失效。
func TestSilenceHalfOpen(t *testing.T) {
	n := mustNotifier(t, nil, 10, 10000, 10, nil)
	a := labels("svc", "db", "name", "x")
	fp := alertstore.Fingerprint(a)
	must(t, n.AddSilence(0, "s1", suppress.Matchers{"svc": "db"}, 10, 20))
	must(t, n.Fire(0, a))
	rc := &recorder{fail: map[int]bool{}}
	t.Log("Tick(10): t=start 仍在静默内（半开区间左闭），V 为空，(a) 不触发")
	if tick(t, n, 10, rc); len(rc.attempts) != 0 {
		t.Fatal("t=start should be silenced")
	}
	if tick(t, n, 19, rc); len(rc.attempts) != 0 {
		t.Fatal("t=19 should be silenced")
	}
	t.Log("Tick(20): t=end 静默失效（右开），(a) 20-0>=W 触发")
	tick(t, n, 20, rc)
	wantNote(t, rc.attempts[0], "", []string{fp}, []string{}, 20)
}

// ExpireSilence 把 end 提前为 min(end,now)；Fire/Resolve 不受静默影响。
func TestSilenceExpire(t *testing.T) {
	n := mustNotifier(t, nil, 0, 10000, 10, nil)
	a := labels("svc", "db", "name", "x")
	fp := alertstore.Fingerprint(a)
	must(t, n.AddSilence(0, "s1", suppress.Matchers{"svc": "db"}, 10, 100))
	must(t, n.Fire(5, a)) // 静默期内 Fire 照常接受
	rc := &recorder{fail: map[int]bool{}}
	if tick(t, n, 20, rc); len(rc.attempts) != 0 {
		t.Fatal("t=20 inside [10,100) should be silenced")
	}
	must(t, n.ExpireSilence(30, "s1")) // end 提前为 min(100,30)=30
	t.Log("Tick(30): 静默被提前结束到 30，t=30 失效")
	tick(t, n, 30, rc)
	wantNote(t, rc.attempts[0], "", []string{fp}, []string{}, 30)
}

// 静默期内组内无发送：lastSet 未变，解除后重现不算新增，不触发 (b)。
func TestSilenceReappearNotNew(t *testing.T) {
	n := mustNotifier(t, nil, 0, 100000, 10, nil)
	a1, a2 := labels("name", "a1"), labels("name", "a2", "svc", "db")
	must(t, n.Fire(0, a1))
	must(t, n.Fire(0, a2))
	rc := &recorder{fail: map[int]bool{}}
	tick(t, n, 0, rc) // lastSet={a1,a2}
	must(t, n.AddSilence(1, "s1", suppress.Matchers{"svc": "db"}, 5, 50))
	if tick(t, n, 10, rc); len(rc.attempts) != 1 {
		t.Fatal("silenced a2, nothing new, no send")
	}
	t.Log("Tick(60): a2 重现但仍在 lastSet 中，不算新增，不发送")
	if tick(t, n, 60, rc); len(rc.attempts) != 1 {
		t.Fatal("reappeared a2 is in lastSet, must not trigger (b)")
	}
}

// 静默期内组内有发送：lastSet 不再含被静默者，解除后重现算新增，触发 (b)。
func TestSilenceReappearNew(t *testing.T) {
	n := mustNotifier(t, nil, 0, 100000, 10, nil)
	a1 := labels("name", "a1")
	a2 := labels("name", "a2", "svc", "db")
	a3 := labels("name", "a3")
	fp1, fp2, fp3 := alertstore.Fingerprint(a1), alertstore.Fingerprint(a2), alertstore.Fingerprint(a3)
	must(t, n.Fire(0, a1))
	must(t, n.Fire(0, a2))
	rc := &recorder{fail: map[int]bool{}}
	tick(t, n, 0, rc) // lastSet={a1,a2}
	must(t, n.AddSilence(1, "s1", suppress.Matchers{"svc": "db"}, 5, 50))
	must(t, n.Fire(2, a3))
	t.Log("Tick(10): a3 新增触发 (b)，lastSet 变为 {a1,a3}（不含被静默的 a2）")
	tick(t, n, 10, rc)
	wantNote(t, rc.attempts[1], "", []string{fp1, fp3}, []string{}, 10)
	t.Log("Tick(60): a2 解除静默后不在 lastSet，算新增，触发 (b)")
	tick(t, n, 60, rc)
	wantNote(t, rc.attempts[2], "", []string{fp1, fp2, fp3}, []string{}, 60)
}

// 抑制不传递不递归：被抑制的源仍有资格抑制他人；被静默的源同样有资格。
func TestInhibitNotTransitive(t *testing.T) {
	rules := []suppress.Rule{
		{Source: suppress.Matchers{"sev": "critical"}, Target: suppress.Matchers{"sev": "warning"}},
		{Source: suppress.Matchers{"sev": "warning"}, Target: suppress.Matchers{"sev": "info"}},
	}
	n := mustNotifier(t, nil, 0, 10000, 10, rules)
	c := labels("name", "c", "sev", "critical")
	w := labels("name", "w", "sev", "warning")
	i := labels("name", "i", "sev", "info")
	fpc := alertstore.Fingerprint(c)
	must(t, n.Fire(0, c))
	must(t, n.Fire(0, w))
	must(t, n.Fire(0, i))
	rc := &recorder{fail: map[int]bool{}}
	t.Log("w 被 c 抑制，但 w 仍有资格抑制 i（不传递=不沿链隐藏源资格），V={c}")
	tick(t, n, 0, rc)
	wantNote(t, rc.attempts[0], "", []string{fpc}, []string{}, 0)

	n2 := mustNotifier(t, nil, 0, 10000, 10, rules)
	must(t, n2.AddSilence(0, "s1", suppress.Matchers{"sev": "critical"}, 0, 100))
	must(t, n2.Fire(0, c))
	must(t, n2.Fire(0, w))
	must(t, n2.Fire(0, i))
	rc2 := &recorder{fail: map[int]bool{}}
	t.Log("c 被静默仍具源资格：w 被 c 抑制、i 被 w 抑制，V 为空，无通知")
	if tick(t, n2, 1, rc2); len(rc2.attempts) != 0 {
		t.Fatal("silenced source must still inhibit")
	}
}

// 互相满足对方源条件的两条告警互相抑制；单条告警不会抑制自己。
func TestMutualInhibit(t *testing.T) {
	rules := []suppress.Rule{
		{Source: suppress.Matchers{"sev": "a"}, Target: suppress.Matchers{"sev": "b"}},
		{Source: suppress.Matchers{"sev": "b"}, Target: suppress.Matchers{"sev": "a"}},
	}
	n := mustNotifier(t, nil, 0, 10000, 10, rules)
	must(t, n.Fire(0, labels("name", "x", "sev", "a")))
	must(t, n.Fire(0, labels("name", "y", "sev", "b")))
	rc := &recorder{fail: map[int]bool{}}
	t.Log("x 与 y 互相抑制，V 为空，无通知")
	if tick(t, n, 0, rc); len(rc.attempts) != 0 {
		t.Fatal("mutual inhibition should hide both")
	}

	n2 := mustNotifier(t, nil, 0, 10000, 10, []suppress.Rule{{
		Source: suppress.Matchers{"sev": "a"}, Target: suppress.Matchers{"sev": "a"},
	}})
	solo := labels("name", "solo", "sev", "a")
	fpSolo := alertstore.Fingerprint(solo)
	must(t, n2.Fire(0, solo))
	rc2 := &recorder{fail: map[int]bool{}}
	t.Log("同指纹不算源，单条告警不抑制自己")
	tick(t, n2, 0, rc2)
	wantNote(t, rc2.attempts[0], "", []string{fpSolo}, []string{}, 0)
}

// equal 中名字两侧都缺失算相同；一侧有一侧无算不同。
func TestEqualBothMissing(t *testing.T) {
	rules := []suppress.Rule{{
		Source: suppress.Matchers{"sev": "critical"},
		Target: suppress.Matchers{"sev": "warning"},
		Equal:  []string{"region"},
	}}
	n := mustNotifier(t, nil, 0, 10000, 10, rules)
	s := labels("name", "s", "sev", "critical")                 // 无 region
	t1 := labels("name", "t1", "sev", "warning")                // 无 region：被抑制
	t2 := labels("name", "t2", "sev", "warning", "region", "e") // 有 region：不被抑制
	fpS, fpT2 := alertstore.Fingerprint(s), alertstore.Fingerprint(t2)
	must(t, n.Fire(0, s))
	must(t, n.Fire(0, t1))
	must(t, n.Fire(0, t2))
	rc := &recorder{fail: map[int]bool{}}
	t.Log("t1 与 s 都缺 region 视为相等被抑制；t2 有 region 与 s 不同，保持可见")
	tick(t, n, 0, rc)
	wantNote(t, rc.attempts[0], "", []string{fpS, fpT2}, []string{}, 0)
}

// 并发调用等价于某个串行顺序：-race 下无数据竞争、无死锁、活跃数不超上限。
func TestConcurrent(t *testing.T) {
	n := mustNotifier(t, []string{"svc"}, 0, 10, 50, nil)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			var now int64
			for i := 0; i < 200; i++ {
				now += int64(id + 1)
				l := labels("svc", "db", "name", "n"+strconv.Itoa((id+i)%10))
				_ = n.Fire(now, l) // 时钟回退等拒绝属预期
				_, _ = n.Tick(now, func(Notification) error { return nil })
				_ = n.Resolve(now, l)
			}
		}(w)
	}
	wg.Wait()
	if n.store.Len() > 50 {
		t.Fatalf("active %d > Amax 50", n.store.Len())
	}
}

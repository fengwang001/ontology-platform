package notify_test

import (
	"errors"
	"log"
	"sort"
	"testing"

	"ontology/alertstore"
	"ontology/notify"
	"ontology/suppress"
)

func fireOK(t *testing.T, n *notify.Notifier, now int64, l alertstore.Labels) {
	if _, err := n.Fire(now, l); err != nil {
		t.Fatal(err)
	}
}

func TestSpecExample(t *testing.T) {
	rule := suppress.InhibitRule{Source: suppress.Matcher{"sev": "critical"},
		Target: suppress.Matcher{"sev": "warning"}, Equal: []string{"svc"}}
	n := mustNew(t, notify.Config{GroupLabels: []string{"svc"}, Wait: 10, Repeat: 100,
		MaxAlerts: 100, Rules: []suppress.InhibitRule{rule}})
	a1 := labels("svc", "db", "sev", "warning", "name", "slow")
	a2 := labels("svc", "db", "sev", "critical", "name", "down")
	fireOK(t, n, 0, a1)
	fireOK(t, n, 0, a2)
	rec := &recorder{errs: map[int]error{}}
	if r5, err := n.Tick(5, rec.send); err != nil || len(r5.Sent) != 0 || len(r5.Failed) != 0 {
		t.Fatal("Tick5 不应发送")
	}
	log.Printf("判定 Tick5: a1 被 a2 抑制, V={a2}, 5-0<W -> 不发送")
	if r, _ := n.Tick(10, rec.send); len(r.Sent) != 1 ||
		len(rec.log[0].Firing) != 1 || rec.log[0].Firing[0] != fp(a2) {
		t.Fatal("Tick10 应满足(a) 且只发 a2")
	}
	resolveOK(t, n, 20, a2)
	if r, _ := n.Tick(21, rec.send); len(r.Sent) != 1 {
		t.Fatalf("Tick21 应满足(b): %+v", r)
	}
	if g := rec.log[1]; len(g.Firing) != 1 || g.Firing[0] != fp(a1) ||
		len(g.Resolved) != 1 || g.Resolved[0] != fp(a2) {
		t.Fatalf("Tick21 通知异常")
	}
	log.Printf("判定 Tick21: V={a1}(不再等 W), D={a2}, 满足(b)")
	if r, _ := n.Tick(120, rec.send); len(r.Sent) != 0 {
		t.Fatal("Tick120 差 99<R 不应发送")
	}
	if r, _ := n.Tick(121, rec.send); len(r.Sent) != 1 {
		t.Fatal("Tick121 恰等 R 应满足(c)")
	}
}

func resolveOK(t *testing.T, n *notify.Notifier, now int64, l alertstore.Labels) {
	if _, err := n.Resolve(now, l); err != nil {
		t.Fatal(err)
	}
}

func assertErrIs(t *testing.T, err, want error, msg string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v", msg, err)
	}
}

func TestFailureRetry(t *testing.T) {
	n := mustNew(t, notify.Config{GroupLabels: []string{"svc"}, Wait: 0, Repeat: 100, MaxAlerts: 10})
	a, b := labels("svc", "db", "x", "1"), labels("svc", "db", "x", "2")
	fireOK(t, n, 0, a)
	fireOK(t, n, 0, b)
	rec := &recorder{errs: map[int]error{0: errors.New("network down")}}
	if r, err := n.Tick(1, rec.send); err != nil || len(r.Failed) != 1 || r.Failed[0] != "db" {
		t.Fatalf("失败判定: %+v %v", r, err)
	}
	rec2 := &recorder{errs: map[int]error{}}
	r2, _ := n.Tick(2, rec2.send)
	if len(r2.Sent) != 1 {
		t.Fatalf("重试应成功: %+v", r2)
	}
	want := []string{fp(a), fp(b)}
	sort.Strings(want)
	if !eqStr(rec2.log[0].Firing, want) || rec2.log[0].At != 2 {
		t.Fatalf("重试内容必须与失败时一致: %+v", rec2.log[0])
	}
	log.Printf("判定 失败重试: At=1 失败状态全保留, At=2 重发同样 Firing=%v", rec2.log[0].Firing)
}

func TestInhibitSingleLevel(t *testing.T) {
	rules := []suppress.InhibitRule{
		{Source: suppress.Matcher{"k": "a"}, Target: suppress.Matcher{"k": "b"}},
		{Source: suppress.Matcher{"k": "b"}, Target: suppress.Matcher{"k": "c"}},
	}
	n := mustNew(t, notify.Config{GroupLabels: []string{"g"}, Wait: 0, Repeat: 100, MaxAlerts: 10, Rules: rules})
	al := map[string]alertstore.Labels{}
	for _, v := range []string{"a", "b", "c"} {
		al[v] = labels("g", "1", "k", v)
		fireOK(t, n, 0, al[v])
	}
	rec := &recorder{errs: map[int]error{}}
	_, _ = n.Tick(0, rec.send)
	if len(rec.log[0].Firing) != 1 || rec.log[0].Firing[0] != fp(al["a"]) {
		t.Fatalf("firing 的 b 虽被抑制仍抑制 c: %v", rec.log[0].Firing)
	}
	resolveOK(t, n, 1, al["a"])
	resolveOK(t, n, 1, al["b"])
	rec2 := &recorder{errs: map[int]error{}}
	_, _ = n.Tick(1, rec2.send)
	if len(rec2.log[0].Firing) != 1 || rec2.log[0].Firing[0] != fp(al["c"]) {
		t.Fatalf("解除 a,b 后应只剩 c: %+v", rec2.log[0])
	}
	log.Printf("判定 抑制一层: a|=b 且 firing(b)|=c, 不递归闭包")
}

func TestSilenceBoundariesAndExpire(t *testing.T) {
	n := mustNew(t, notify.Config{GroupLabels: []string{"g"}, Wait: 0, Repeat: 15, MaxAlerts: 10})
	fireOK(t, n, 0, labels("g", "1", "k", "v"))
	if err := n.AddSilence(0, "s1", suppress.Matcher{"k": "v"}, 10, 20); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{errs: map[int]error{}}
	if r, _ := n.Tick(10, rec.send); len(r.Sent) != 0 {
		t.Fatal("t=10 应静默")
	}
	if r, _ := n.Tick(19, rec.send); len(r.Sent) != 0 {
		t.Fatal("t=19 应静默")
	}
	if r, _ := n.Tick(20, rec.send); len(r.Sent) != 1 {
		t.Fatal("t=20 半开边界应解除并发送")
	}
	log.Printf("判定 静默半开: [10,20), t=10/19 无发送, t=20 发送")
	_ = n.AddSilence(20, "s2", suppress.Matcher{"k": "v"}, 30, 100)
	if r, _ := n.Tick(30, rec.send); len(r.Sent) != 0 {
		t.Fatal("t=30 静默中")
	}
	_ = n.ExpireSilence(35, "s2")
	if r, _ := n.Tick(35, rec.send); len(r.Sent) != 1 {
		t.Fatal("Expire 后 end=35, t=35 解除且 35-20>=R")
	}
	if err := n.ExpireSilence(35, "s2"); err != nil {
		t.Fatalf("已结束静默再 Expire 应为 nil, got %v", err)
	}
	if !errors.Is(n.ExpireSilence(35, "nope"), notify.ErrSilenceMissing) {
		t.Fatal("Expire 不存在编号应报不存在")
	}
	if !errors.Is(n.AddSilence(35, "s1", suppress.Matcher{"k": "v"}, 0, 1), notify.ErrSilenceExists) {
		t.Fatal("重复编号应报冲突")
	}
}

func TestSilenceNewFingerprint(t *testing.T) {
	n := mustNew(t, notify.Config{GroupLabels: []string{"g"}, Wait: 5, Repeat: 1000, MaxAlerts: 10})
	fireOK(t, n, 0, labels("g", "1", "k", "v"))
	_ = n.AddSilence(0, "s", suppress.Matcher{"k": "v"}, 5, 10)
	rec := &recorder{errs: map[int]error{}}
	if r, _ := n.Tick(5, rec.send); len(r.Sent) != 0 {
		t.Fatal("静默中不发送")
	}
	if r, _ := n.Tick(9, rec.send); len(r.Sent) != 0 {
		t.Fatal("静默中不发送")
	}
	if r, _ := n.Tick(10, rec.send); len(r.Sent) != 1 || rec.log[0].At != 10 {
		t.Fatalf("期内无发送, 解除后按首次等待: %+v", r)
	}
	n2 := mustNew(t, notify.Config{GroupLabels: []string{"g"}, Wait: 0, Repeat: 1000, MaxAlerts: 10})
	fireOK(t, n2, 0, labels("g", "1", "k", "v"))
	_ = n2.AddSilence(0, "s", suppress.Matcher{"k": "v"}, 5, 10)
	rec2 := &recorder{errs: map[int]error{}}
	if r, _ := n2.Tick(1, rec2.send); len(r.Sent) != 1 {
		t.Fatal("静默前应发送一次")
	}
	if r, _ := n2.Tick(6, rec2.send); len(r.Sent) != 0 {
		t.Fatal("静默中 V/D 均空, 不发送")
	}
	fireOK(t, n2, 7, labels("g", "1", "k", "w"))
	if r, _ := n2.Tick(10, rec2.send); len(r.Sent) != 1 ||
		!contains(rec2.log[1].Firing, fp(labels("g", "1", "k", "w"))) {
		t.Fatalf("静默中新指纹 w 解除后属新增应立即发送: %+v", rec2.log)
	}
	log.Printf("判定 静默新增: 无发送→首等; 期内新指纹→变化(b)")
}

func TestLimitRejectionLeavesState(t *testing.T) {
	n := mustNew(t, notify.Config{GroupLabels: []string{"g"}, Wait: 0, Repeat: 100, MaxAlerts: 2})
	a, b, c := labels("g", "1", "i", "a"), labels("g", "1", "i", "b"), labels("g", "1", "i", "c")
	fireOK(t, n, 5, a)
	fireOK(t, n, 5, b)
	_, eLimit := n.Fire(5, c)
	assertErrIs(t, eLimit, notify.ErrAlertLimit, "满额新建应拒绝")
	_, eBack := n.Fire(4, c)
	assertErrIs(t, eBack, notify.ErrClockRollback, "被拒不推进时钟, now=4 应回退")
	fr, err := n.Fire(5, a)
	if err != nil || !fr.Deduped || fr.Alert.Deduped != 1 {
		t.Fatalf("重复 Fire 应 Deduped=1: %+v %v", fr, err)
	}
	_, eNF := n.Resolve(5, c)
	assertErrIs(t, eNF, notify.ErrNotFiring, "Resolve 不存在应报非 firing")
	log.Printf("判定 上限: Amax=2 时 c 被拒不驱逐; 时钟停 5; a.Deduped=1")
}

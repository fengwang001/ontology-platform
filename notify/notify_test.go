package notify

import (
	"errors"
	"reflect"
	"testing"

	"ontology/alertstore"
	"ontology/suppress"
)

var errSend = errors.New("send failed")

func labels(pairs ...string) alertstore.Labels {
	l := make(alertstore.Labels, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		l[pairs[i]] = pairs[i+1]
	}
	return l
}

func mustNotifier(t *testing.T, g []string, w, r int64, amax int, rules []suppress.Rule) *Notifier {
	t.Helper()
	n, err := New(g, w, r, amax, rules)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return n
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

type recorder struct {
	attempts []Notification
	fail     map[int]bool
}

func (rc *recorder) send(n Notification) error {
	idx := len(rc.attempts)
	rc.attempts = append(rc.attempts, n)
	if rc.fail[idx] {
		return errSend
	}
	return nil
}

func tick(t *testing.T, n *Notifier, now int64, rc *recorder) Result {
	t.Helper()
	res, err := n.Tick(now, rc.send)
	if err != nil {
		t.Fatalf("Tick(%d): %v", now, err)
	}
	t.Logf("Tick(%d) -> Sent=%v Failed=%v attempts=%d", now, res.Sent, res.Failed, len(rc.attempts))
	return res
}

func wantNote(t *testing.T, got Notification, group string, firing, resolved []string, at int64) {
	t.Helper()
	want := Notification{Group: group, Firing: firing, Resolved: resolved, At: at}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notification = %+v, want %+v", got, want)
	}
	t.Logf("notification OK: %+v", got)
}

// 规格书示例：抑制、首次等待 W、Resolved 报告、恰等 R 重复。
func TestSpecExample(t *testing.T) {
	rules := []suppress.Rule{{
		Source: suppress.Matchers{"sev": "critical"},
		Target: suppress.Matchers{"sev": "warning"},
		Equal:  []string{"svc"},
	}}
	n := mustNotifier(t, []string{"svc"}, 10, 100, 100, rules)
	a1 := labels("svc", "db", "sev", "warning", "name", "slow")
	a2 := labels("svc", "db", "sev", "critical", "name", "down")
	fp1, fp2 := alertstore.Fingerprint(a1), alertstore.Fingerprint(a2)
	must(t, n.Fire(0, a1))
	must(t, n.Fire(0, a2))
	rc := &recorder{fail: map[int]bool{}}

	t.Log("Tick(5): a1 被 a2 抑制，V={a2}，5-0<W=10 不满足 (a)，无通知")
	if res := tick(t, n, 5, rc); len(res.Sent)+len(res.Failed) != 0 || len(rc.attempts) != 0 {
		t.Fatalf("Tick(5) should not send: %+v", res)
	}
	t.Log("Tick(10): 满足 (a)，发 Firing=[a2]")
	tick(t, n, 10, rc)
	wantNote(t, rc.attempts[0], "db", []string{fp2}, []string{}, 10)

	must(t, n.Resolve(20, a2))
	t.Log("Tick(21): V={a1}、D={a2}，满足 (b)，发 Firing=[a1] Resolved=[a2]")
	tick(t, n, 21, rc)
	wantNote(t, rc.attempts[1], "db", []string{fp1}, []string{fp2}, 21)

	t.Log("Tick(120): 121-21=99<R=100 不满足 (c)，无通知")
	if res := tick(t, n, 120, rc); len(rc.attempts) != 2 {
		t.Fatalf("Tick(120) should not send: %+v", res)
	}
	t.Log("Tick(121): 恰等 R，满足 (c)，发 Firing=[a1]")
	tick(t, n, 121, rc)
	wantNote(t, rc.attempts[2], "db", []string{fp1}, []string{}, 121)
}

// send 失败则整组状态不变，下次 Tick 重发同样内容；未发送的组才清除无需报告的 resolved。
func TestRetrySameContent(t *testing.T) {
	n := mustNotifier(t, nil, 0, 1000, 10, nil)
	a1 := labels("name", "a1")
	a2 := labels("name", "a2")
	a3 := labels("name", "a3")
	fp1, fp2, fp3 := alertstore.Fingerprint(a1), alertstore.Fingerprint(a2), alertstore.Fingerprint(a3)
	must(t, n.Fire(0, a1))
	must(t, n.Fire(0, a2))
	rc := &recorder{fail: map[int]bool{}}
	tick(t, n, 0, rc) // lastSet={fp1,fp2}
	wantNote(t, rc.attempts[0], "", []string{fp1, fp2}, []string{}, 0)

	must(t, n.Resolve(5, a2)) // 进入 D
	must(t, n.Fire(5, a3))
	must(t, n.Resolve(6, a3)) // resolved 且不在 lastSet：本应直接清除
	rc.fail[1] = true
	t.Log("Tick(10): 满足 (b) 但 send 失败，a2 与 a3 都保留")
	res := tick(t, n, 10, rc)
	wantNote(t, rc.attempts[1], "", []string{fp1}, []string{fp2}, 10)
	if !reflect.DeepEqual(res.Failed, []string{""}) {
		t.Fatalf("Failed = %v", res.Failed)
	}
	if n.store.Get(fp2) == nil || n.store.Get(fp3) == nil {
		t.Fatal("failed send must keep resolved alerts")
	}
	t.Log("Tick(11): 重试，内容与失败那次完全一致；成功后 a3 被静默清除")
	tick(t, n, 11, rc)
	got, prev := rc.attempts[2], rc.attempts[1]
	if got.Group != prev.Group || !reflect.DeepEqual(got.Firing, prev.Firing) ||
		!reflect.DeepEqual(got.Resolved, prev.Resolved) {
		t.Fatalf("retry = %+v, want same payload as %+v", got, prev)
	}
	if n.store.Get(fp2) != nil || n.store.Get(fp3) != nil {
		t.Fatal("resolved alerts should be absorbed after successful send")
	}
}

// W 只约束首次发送；组已有 lastSent 后新指纹立即经 (b) 触发。
func TestWaitOnlyFirst(t *testing.T) {
	n := mustNotifier(t, nil, 100, 10000, 10, nil)
	a1, a2 := labels("name", "x1"), labels("name", "x2")
	fp1, fp2 := alertstore.Fingerprint(a1), alertstore.Fingerprint(a2)
	must(t, n.Fire(0, a1))
	rc := &recorder{fail: map[int]bool{}}
	if tick(t, n, 99, rc); len(rc.attempts) != 0 {
		t.Fatal("99 < W=100, should wait")
	}
	tick(t, n, 100, rc)
	wantNote(t, rc.attempts[0], "", []string{fp1}, []string{}, 100)
	must(t, n.Fire(100, a2))
	t.Log("Tick(101): a2 只等待 1ms，远小于 W，但 (b) 新指纹立即触发")
	tick(t, n, 101, rc)
	wantNote(t, rc.attempts[1], "", []string{fp1, fp2}, []string{}, 101)
}

// 组内告警清空后组状态删除，新告警重新按 (a) 等待 W。
func TestGroupStateReset(t *testing.T) {
	n := mustNotifier(t, nil, 50, 10000, 10, nil)
	a1 := labels("name", "g1")
	fp1 := alertstore.Fingerprint(a1)
	must(t, n.Fire(0, a1))
	rc := &recorder{fail: map[int]bool{}}
	tick(t, n, 50, rc)
	must(t, n.Resolve(60, a1))
	tick(t, n, 61, rc) // 报告 Resolved 后组内无告警，组状态删除
	wantNote(t, rc.attempts[1], "", []string{}, []string{fp1}, 61)
	if len(n.states) != 0 {
		t.Fatalf("group state should be deleted, got %d", len(n.states))
	}
	must(t, n.Fire(70, a1))
	if tick(t, n, 100, rc); len(rc.attempts) != 2 {
		t.Fatal("new alert must wait W again after group reset")
	}
	tick(t, n, 120, rc) // 120-70=50>=W
	wantNote(t, rc.attempts[2], "", []string{fp1}, []string{}, 120)
}

// 多组按组键字节序处理。
func TestGroupOrder(t *testing.T) {
	n := mustNotifier(t, []string{"svc"}, 0, 1000, 10, nil)
	must(t, n.Fire(0, labels("svc", "b", "name", "n1")))
	must(t, n.Fire(0, labels("svc", "a", "name", "n2")))
	rc := &recorder{fail: map[int]bool{}}
	res := tick(t, n, 0, rc)
	if !reflect.DeepEqual(res.Sent, []string{"a", "b"}) {
		t.Fatalf("Sent = %v, want [a b]", res.Sent)
	}
	if rc.attempts[0].Group != "a" || rc.attempts[1].Group != "b" {
		t.Fatalf("send order = %q,%q", rc.attempts[0].Group, rc.attempts[1].Group)
	}
}

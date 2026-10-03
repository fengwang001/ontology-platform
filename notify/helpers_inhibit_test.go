package notify_test

import (
	"errors"
	"log"
	"testing"

	"ontology/alertstore"
	"ontology/notify"
	"ontology/suppress"
)

func mustNew(t *testing.T, cfg notify.Config) *notify.Notifier {
	t.Helper()
	n, err := notify.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return n
}

type recorder struct {
	errs map[int]error
	log  []notify.Notification
}

func (r *recorder) send(n notify.Notification) error {
	log.Printf("send group=%q at=%d firing=%v resolved=%v", n.Group, n.At, n.Firing, n.Resolved)
	if err := r.errs[len(r.log)]; err != nil {
		log.Printf("  -> 发送失败: %v（该组状态保留, 下次 Tick 重算重发）", err)
		return err
	}
	log.Printf("  -> 发送成功: lastSent=%d lastSet=%v", n.At, n.Firing)
	r.log = append(r.log, n)
	return nil
}

func labels(kv ...string) alertstore.Labels {
	l := alertstore.Labels{}
	for i := 0; i < len(kv); i += 2 {
		l[kv[i]] = kv[i+1]
	}
	return l
}

func fp(l alertstore.Labels) string { return alertstore.Fingerprint(l) }

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func eqStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func noteEqual(a, b notify.Notification) bool {
	return a.Group == b.Group && a.At == b.At &&
		eqStr(a.Firing, b.Firing) && eqStr(a.Resolved, b.Resolved)
}

// ---- 抑制语义 ----

func TestMutualInhibit(t *testing.T) {
	rules := []suppress.InhibitRule{
		{Source: suppress.Matcher{"k": "a"}, Target: suppress.Matcher{"k": "b"}},
		{Source: suppress.Matcher{"k": "b"}, Target: suppress.Matcher{"k": "a"}},
	}
	n := mustNew(t, notify.Config{GroupLabels: []string{"g"}, Wait: 0, Repeat: 100, MaxAlerts: 10, Rules: rules})
	a, b := labels("g", "1", "k", "a"), labels("g", "1", "k", "b")
	if _, err := n.Fire(0, a); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Fire(0, b); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{errs: map[int]error{}}
	r, _ := n.Tick(0, rec.send)
	if len(r.Sent) != 0 {
		t.Fatalf("互抑制 V 为空, 不应发送: %+v", rec.log)
	}
	log.Printf("判定 互抑制: a,b 互相满足源/目标, 双方皆不可见, 不发送")
}

// equal 双侧都缺失视为相等从而命中抑制；仅一侧缺失则不抑制。
func TestEqualMissingBothSides(t *testing.T) {
	rule := suppress.InhibitRule{
		Source: suppress.Matcher{"k": "a"}, Target: suppress.Matcher{"k": "b"},
		Equal: []string{"zone"},
	}
	n := mustNew(t, notify.Config{GroupLabels: []string{"g"}, Wait: 0, Repeat: 100,
		MaxAlerts: 10, Rules: []suppress.InhibitRule{rule}})
	a, b := labels("g", "1", "k", "a"), labels("g", "1", "k", "b")
	if _, err := n.Fire(0, a); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Fire(0, b); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{errs: map[int]error{}}
	r, _ := n.Tick(0, rec.send)
	if len(r.Sent) != 1 || !contains(rec.log[0].Firing, fp(a)) ||
		contains(rec.log[0].Firing, fp(b)) {
		t.Fatalf("双侧缺失 zone 视为相等, b 被抑制: %+v", rec.log)
	}
	if _, err := n.Resolve(1, a); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Fire(2, labels("g", "1", "k", "a", "zone", "z1")); err != nil {
		t.Fatal(err)
	}
	rec2 := &recorder{errs: map[int]error{}}
	_, _ = n.Tick(2, rec2.send)
	if !contains(rec2.log[0].Firing, fp(b)) {
		t.Fatalf("仅 a 有 zone、b 缺失, 不应抑制: %+v", rec2.log[0])
	}
	log.Printf("判定 equal: 双侧缺失=空串相等; 单侧缺失=不等")
}

// 源自身被静默仍具抑制资格。
func TestSilencedSourceStillInhibits(t *testing.T) {
	rule := suppress.InhibitRule{
		Source: suppress.Matcher{"k": "a"}, Target: suppress.Matcher{"k": "b"},
	}
	n := mustNew(t, notify.Config{GroupLabels: []string{"g"}, Wait: 0, Repeat: 1000,
		MaxAlerts: 10, Rules: []suppress.InhibitRule{rule}})
	if _, err := n.Fire(0, labels("g", "1", "k", "a")); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Fire(0, labels("g", "1", "k", "b")); err != nil {
		t.Fatal(err)
	}
	if err := n.AddSilence(0, "s", suppress.Matcher{"k": "a"}, 0, 10); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{errs: map[int]error{}}
	if r, _ := n.Tick(5, rec.send); len(r.Sent) != 0 {
		t.Fatalf("a 被静默但仍抑制 b, V 为空: %+v", rec.log)
	}
	log.Printf("判定 源资格: 被静默的 firing a 仍抑制 b")
}

// 参数非法优先：nil send、非法标签、构造越界均在时钟/状态检查之前报出。
func TestInvalidArgumentPriority(t *testing.T) {
	if _, err := notify.New(notify.Config{Wait: 1 << 31, MaxAlerts: 1}); !errors.Is(err, notify.ErrInvalidArgument) {
		t.Fatalf("W 越界应非法: %v", err)
	}
	if _, err := notify.New(notify.Config{MaxAlerts: 0}); !errors.Is(err, notify.ErrInvalidArgument) {
		t.Fatalf("Amax=0 应非法: %v", err)
	}
	n := mustNew(t, notify.Config{GroupLabels: []string{"g"}, MaxAlerts: 10})
	if _, err := n.Tick(1, nil); !errors.Is(err, notify.ErrInvalidArgument) {
		t.Fatalf("nil send 应非法: %v", err)
	}
	if _, err := n.Fire(-1, labels("g", "1")); !errors.Is(err, notify.ErrInvalidArgument) {
		t.Fatalf("负 now 应非法: %v", err)
	}
	if _, err := n.Fire(0, alertstore.Labels{"": "v"}); !errors.Is(err, notify.ErrInvalidArgument) {
		t.Fatalf("空键应非法: %v", err)
	}
	if _, err := n.Fire(0, alertstore.Labels{}); !errors.Is(err, notify.ErrInvalidArgument) {
		t.Fatalf("空标签集应非法: %v", err)
	}
	if err := n.AddSilence(0, "", suppress.Matcher{"k": "v"}, 0, 1); !errors.Is(err, notify.ErrInvalidArgument) {
		t.Fatalf("空 id 应非法: %v", err)
	}
	if err := n.AddSilence(0, "x", suppress.Matcher{"k": "v"}, 5, 5); !errors.Is(err, notify.ErrInvalidArgument) {
		t.Fatalf("start>=end 应非法: %v", err)
	}
	log.Printf("判定 参数非法: nil send/越界/空值/start>=end 均优先报 ErrInvalidArgument")
}

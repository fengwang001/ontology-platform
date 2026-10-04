package rotate

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/cert"
)

func newSvc(t *testing.T, g, ttl, w int64) *Service {
	t.Helper()
	s, err := New(g, ttl, w)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func stOf(s *Service, serial string) cert.State {
	r := s.bySerial[serial]
	if r == nil {
		return -1
	}
	return r.st
}

// mustOK 返回校验闭包；多值返回可作为闭包的唯一实参直接展开。
func mustOK(t *testing.T) func([]string, error) []string {
	t.Helper()
	return func(k []string, err error) []string {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return k
	}
}

func kicks(k []string) []string {
	if k == nil {
		return []string{}
	}
	return k
}

// TestWorkedExample 复现题述示例：续期窗口取等、max(now,nb)、na 封顶、被拒不落地。
func TestWorkedExample(t *testing.T) {
	s := newSvc(t, 40, 100, 100)
	m := newSessMgr(s)

	mustOK(t)(s.Issue("d", "s1", 0, 1000, 0))
	if stOf(s, "s1") != cert.Active {
		t.Fatal("s1 should be Active")
	}
	if _, err := s.Issue("d", "s2", 950, 2000, 899); !errors.Is(err, cert.ErrTooEarly) {
		t.Fatalf("899 want ErrTooEarly, got %v", err)
	}
	mustOK(t)(s.Issue("d", "s2", 950, 2000, 900))
	r2 := s.bySerial["s2"]
	if r2.st != cert.Pending || r2.lapseAt != 1050 {
		t.Fatalf("s2: st=%v lapseAt=%d", r2.st, r2.lapseAt)
	}

	if _, err := m.Connect("d", "s2", 949); !errors.Is(err, cert.ErrNotYet) {
		t.Fatalf("949 want ErrNotYet, got %v", err)
	}
	mustOK(t)(m.Connect("d", "s2", 950))
	if stOf(s, "s2") != cert.Active || stOf(s, "s1") != cert.Retiring {
		t.Fatalf("after confirm s2=%v s1=%v", stOf(s, "s2"), stOf(s, "s1"))
	}
	if s.bySerial["s1"].retireAt != 990 {
		t.Fatalf("retireAt=%d want 990", s.bySerial["s1"].retireAt)
	}

	// Retiring 宽限内接管会话，不进踢线清单。
	if k := mustOK(t)(m.Connect("d", "s1", 989)); len(k) != 0 {
		t.Fatalf("takeover kicked %v", k)
	}
	if ser, ok := m.SessionSerial("d"); !ok || ser != "s1" {
		t.Fatalf("session=%q,%v want s1", ser, ok)
	}

	// 990 到点报 ErrRetired；被拒：状态、会话、时钟、popped 均不变。
	if _, err := m.Connect("d", "s1", 990); !errors.Is(err, cert.ErrRetired) {
		t.Fatalf("990 want ErrRetired, got %v", err)
	}
	if stOf(s, "s1") != cert.Retiring {
		t.Fatal("rejected op must not settle state")
	}
	if ser, _ := m.SessionSerial("d"); ser != "s1" {
		t.Fatal("rejected op must not kick session")
	}
	if s.Popped() != 0 {
		t.Fatalf("rejected op counted popped=%d", s.Popped())
	}

	// 下一个被接受的操作（为其他设备签发）结算 s1 并报告 Kicked=[d]。
	k := mustOK(t)(s.Issue("other", "s9", 0, 2000, 991))
	if !reflect.DeepEqual(kicks(k), []string{"d"}) {
		t.Fatalf("Kicked=%v want [d]", k)
	}
	if stOf(s, "s1") != cert.Retired {
		t.Fatalf("s1=%v want Retired", stOf(s, "s1"))
	}
	if _, ok := m.SessionSerial("d"); ok {
		t.Fatal("session d should be kicked")
	}
	if s.Popped() != 1 {
		t.Fatalf("popped=%d want 1", s.Popped())
	}
}

// TestRetireCappedByNa 验证 now=970 首次确认时 retireAt=min(1010,1000)=1000。
func TestRetireCappedByNa(t *testing.T) {
	s := newSvc(t, 40, 1000, 1000)
	m := newSessMgr(s)
	mustOK(t)(s.Issue("d", "s1", 0, 1000, 0))
	mustOK(t)(s.Issue("d", "s2", 950, 2000, 900))
	mustOK(t)(m.Connect("d", "s2", 970))
	if at := s.bySerial["s1"].retireAt; at != 1000 {
		t.Fatalf("retireAt=%d want 1000", at)
	}
}

// TestPendingLapsedThenReissue 验证 Pending 到点 Lapsed、准入报 ErrLapsed、之后可再 Issue。
func TestPendingLapsedThenReissue(t *testing.T) {
	s := newSvc(t, 40, 100, 100)
	m := newSessMgr(s)
	mustOK(t)(s.Issue("d", "s1", 0, 1000, 0))
	mustOK(t)(s.Issue("d", "s2", 950, 2000, 900)) // lapseAt=1050

	if _, err := m.Connect("d", "s2", 1050); !errors.Is(err, cert.ErrLapsed) {
		t.Fatalf("1050 want ErrLapsed, got %v", err)
	}
	// 被拒不落地：s2 仍是 Pending；下一个接受操作完成结算。
	if stOf(s, "s2") != cert.Pending {
		t.Fatal("rejected connect must not settle")
	}
	mustOK(t)(s.Issue("x", "x1", 0, 10, 1050))
	if stOf(s, "s2") != cert.Lapsed {
		t.Fatalf("s2=%v want Lapsed", stOf(s, "s2"))
	}
	if _, err := m.Connect("d", "s2", 1060); !errors.Is(err, cert.ErrLapsed) {
		t.Fatalf("post-settle want ErrLapsed, got %v", err)
	}
	// Lapsed 后 Pending 槽已空，可再签。
	mustOK(t)(s.Issue("d", "s3", 1000, 3000, 1060))
	if _, p, _ := s.devState("d"); p != "s3" {
		t.Fatalf("pending=%s want s3", p)
	}
}

// TestIssueRejectOrder 验证 Issue 的拒绝次序：非法 > 时钟回退 > 重复 > Pending > 窗口。
func TestIssueRejectOrder(t *testing.T) {
	s := newSvc(t, 10, 100, 100)
	mustOK(t)(s.Issue("d", "s1", 0, 1000, 0))

	if _, err := s.Issue("d", "", 0, 10, 0); !errors.Is(err, cert.ErrInvalid) {
		t.Fatalf("empty serial: %v", err)
	}
	if _, err := s.Issue("d", "a", 5, 5, 0); !errors.Is(err, cert.ErrInvalid) {
		t.Fatalf("nb==na: %v", err)
	}
	s.setLastNow(50)
	if _, err := s.Issue("d", "s1", 0, 10, 10); !errors.Is(err, cert.ErrClockBack) {
		t.Fatalf("clockback should beat dup, got %v", err)
	}
	s.setLastNow(0)
	mustOK(t)(s.Issue("d", "s2", 950, 2000, 900))
	if _, err := s.Issue("d", "s1", 0, 2000, 900); !errors.Is(err, cert.ErrDupSerial) {
		t.Fatalf("dup should beat pending, got %v", err)
	}
	if _, err := s.Issue("d", "s4", 0, 2000, 900); !errors.Is(err, cert.ErrPendingExists) {
		t.Fatalf("pending should beat early, got %v", err)
	}
	// 另起干净设备验证“未到窗口”：Active na=5000,W=100 => 窗口起点 4900。
	mustOK(t)(s.Issue("e", "e1", 0, 5000, 900))
	if _, err := s.Issue("e", "e2", 0, 6000, 905); !errors.Is(err, cert.ErrTooEarly) {
		t.Fatalf("too early, got %v", err)
	}
	if stOf(s, "e2") != -1 {
		t.Fatal("rejected issue must not create cert")
	}
	// 取等允许：窗口起点签发成功。
	mustOK(t)(s.Issue("e", "e2", 0, 6000, 4900))
}

// TestRevokeOrderAndSemantics 覆盖未知/终态报错、吊销 Active 后 Pending 上位、Retiring 不回升。
func TestRevokeOrderAndSemantics(t *testing.T) {
	s := newSvc(t, 100, 1000, 1_000_000_000)
	m := newSessMgr(s)
	if _, err := s.Revoke("", 0); !errors.Is(err, cert.ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
	if _, err := s.Revoke("nope", 0); !errors.Is(err, cert.ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}
	mustOK(t)(s.Issue("d", "s1", 0, 1000, 0))
	mustOK(t)(s.Issue("d", "s2", 100, 2000, 500))
	mustOK(t)(m.Connect("d", "s1", 500))
	if k := mustOK(t)(s.Revoke("s1", 600)); !reflect.DeepEqual(kicks(k), []string{"d"}) {
		t.Fatalf("revoke kick=%v", k)
	}
	if stOf(s, "s2") != cert.Pending {
		t.Fatal("pending must remain after active revoked")
	}
	mustOK(t)(m.Connect("d", "s2", 610))
	if a, p, r := s.devState("d"); a != "s2" || p != "" || r != "" {
		t.Fatalf("slots a=%s p=%s r=%s, want s2//", a, p, r)
	}
	// 终态再吊销报错，且未知序列号在时钟回退之后判定。
	if _, err := s.Revoke("s1", 609); !errors.Is(err, cert.ErrClockBack) {
		t.Fatalf("clockback beats final, got %v", err)
	}
	if _, err := s.Revoke("s1", 610); !errors.Is(err, cert.ErrFinal) {
		t.Fatalf("final: %v", err)
	}

	// Retiring 不回升场景：确认后再吊销新 Active。
	s2 := newSvc(t, 1000, 1000, 1_000_000_000)
	m2 := newSessMgr(s2)
	mustOK(t)(s2.Issue("e", "a", 0, 100000, 0))
	mustOK(t)(s2.Issue("e", "b", 0, 100000, 10))
	mustOK(t)(m2.Connect("e", "b", 20)) // a Retiring(retireAt=1020)
	mustOK(t)(s2.Revoke("b", 30))       // 设备没有 Active
	if a, _, r := s2.devState("e"); a != "" || r != "a" {
		t.Fatalf("after revoke active a=%q r=%q", a, r)
	}
	mustOK(t)(s2.Issue("e", "c", 0, 100000, 40)) // 无 Active -> 直接 Active
	if stOf(s2, "c") != cert.Active {
		t.Fatal("new cert should be Active directly")
	}
	if stOf(s2, "a") != cert.Retiring {
		t.Fatal("old Retiring must not rebound")
	}
	mustOK(t)(s2.Issue("z", "zz", 0, 100000, 1020))
	if stOf(s2, "a") != cert.Retired {
		t.Fatalf("a=%v want Retired at retireAt", stOf(s2, "a"))
	}
}

// TestThreeCertsChain 三张证并存，确认新证时最老者立即 Retired 且踢线只报一次。
func TestThreeCertsChain(t *testing.T) {
	s := newSvc(t, 1000, 1000, 1_000_000_000)
	m := newSessMgr(s)
	mustOK(t)(s.Issue("d", "a", 0, 100000, 0))
	mustOK(t)(s.Issue("d", "b", 0, 100000, 10))
	mustOK(t)(m.Connect("d", "b", 20)) // a Retiring(retireAt=1020)
	mustOK(t)(s.Issue("d", "c", 0, 100000, 30))
	mustOK(t)(m.Connect("d", "a", 40)) // 会话挂到最老的 a 上
	k := mustOK(t)(m.Connect("d", "c", 50))
	if stOf(s, "a") != cert.Retired {
		t.Fatalf("a=%v want immediate Retired", stOf(s, "a"))
	}
	if stOf(s, "b") != cert.Retiring || stOf(s, "c") != cert.Active {
		t.Fatalf("b=%v c=%v", stOf(s, "b"), stOf(s, "c"))
	}
	if !reflect.DeepEqual(kicks(k), []string{"d"}) {
		t.Fatalf("kicked=%v want [d]", k)
	}
}

// TestPoppedBound 对照 100/10000 设备、同样 2 次迁移：弹出数恒为 2，与规模无关。
func TestPoppedBound(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(devices(n).label(), func(t *testing.T) {
			s := newSvc(t, 100, 100, 100)
			for i := 0; i < n; i++ {
				d := deviceName(i, n)
				s.seedActive(d+"-a", d, 0, cert.MaxTime)
			}
			// 仅这两张安排在本时刻到期（Retiring->Retired）。
			s.seedRetiring("m1", "migrate-1", 0, 500, 1000)
			s.seedRetiring("m2", "migrate-2", 0, 500, 1000)
			before := s.heapLen()
			k := mustOK(t)(s.Issue("newdev", "n1", 0, 2000, 1000))
			got := s.Popped()
			if got != 2 {
				t.Fatalf("n=%d popped=%d want 2 (heap before=%d)", n, got, before)
			}
			if len(kicks(k)) != 0 { // 无会话钩子，结算也不应产生踢线名
				t.Fatalf("kicked=%v", k)
			}
			// 约束：弹出数 <= 实际迁移数+1；此处严格相等。
			if got > 2+1 {
				t.Fatalf("bound violated: %d", got)
			}
		})
	}
}

type devices int

func (d devices) label() string {
	if d == 100 {
		return "100"
	}
	return "10000"
}

// deviceName 给设备定宽命名，使字节序与编号一致。
func deviceName(i, n int) string {
	width := 1
	for p := 10; p < n; p *= 10 {
		width++
	}
	return "d" + padLeft(i, width)
}

func padLeft(i, w int) string {
	b := make([]byte, w)
	for k := w - 1; k >= 0; k-- {
		b[k] = byte('0' + i%10)
		i /= 10
	}
	return string(b)
}

func BenchmarkSettle100(b *testing.B) { benchmarkSettle(b, 100) }
func BenchmarkSettle10000(b *testing.B) {
	b.Skip("10000 档由 TestPoppedBound 覆盖；需要时去掉 Skip")
	benchmarkSettle(b, 10000)
}

func benchmarkSettle(b *testing.B, n int) {
	for i := 0; i < b.N; i++ {
		s, _ := New(100, 100, 100)
		for j := 0; j < n; j++ {
			d := deviceName(j, n)
			s.seedActive(d+"-a", d, 0, cert.MaxTime)
		}
		s.seedRetiring("m1", "migrate-1", 0, 500, 1000)
		s.seedRetiring("m2", "migrate-2", 0, 500, 1000)
		if _, err := s.Issue("newdev", "n1", 0, 2000, 1000); err != nil {
			b.Fatal(err)
		}
		if s.Popped() != 2 {
			b.Fatalf("popped=%d", s.Popped())
		}
	}
}

// TestConcurrentLinearizability 并发交错调用：结果不 panic、无数据竞争（-race 守护），
// 结束后每台设备至多一张 Active/Pending/Retiring，会话只用 Active/Retiring。
func TestConcurrentLinearizability(t *testing.T) {
	s := newSvc(t, 200, 2000, 1_000_000_000)
	m := newSessMgr(s)

	// 先为每台设备准备一张 Active。
	const devs = 8
	for d := 0; d < devs; d++ {
		dev := fmt.Sprintf("d%d", d)
		mustOK(t)(s.Issue(dev, dev+"-a0", 0, 1_000_000_000, 0))
	}

	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				d := w % devs
				dev := fmt.Sprintf("d%d", d)
				now := int64((i + 1) * 3) // 单调时间，避免大量时钟回退
				switch i % 6 {
				case 0:
					_, _ = s.Issue(dev, fmt.Sprintf("%s-p%d-%d", dev, w, i), 0, 1_000_000_000, now)
				case 1:
					_, _ = m.Connect(dev, dev+"-a0", now)
				case 2:
					_, _ = m.Disconnect(dev, now)
				case 3:
					snaps := s.Snapshot()
					if len(snaps) == 0 {
						t.Error("empty snapshot")
					}
				case 4:
					_ = s.Popped()
				case 5:
					_, _ = s.Revoke(dev+"-a0", now)
				}
			}
		}(w)
	}
	wg.Wait()

	// 不变量：每设备每状态至多一张。
	counts := map[string]map[cert.State]int{}
	for _, r := range s.bySerial {
		cm := counts[r.cert.Dev]
		if cm == nil {
			cm = map[cert.State]int{}
			counts[r.cert.Dev] = cm
		}
		cm[r.st]++
	}
	for dev, cm := range counts {
		for _, st := range []cert.State{cert.Active, cert.Pending, cert.Retiring} {
			if cm[st] > 1 {
				t.Fatalf("dev=%s has %d %s certs", dev, cm[st], st)
			}
		}
	}
	// 会话所用证书必须处于 Active/Retiring。
	for dev, serial := range m.sessions {
		st := stOf(s, serial)
		if st != cert.Active && st != cert.Retiring {
			t.Fatalf("dev=%s session on %s in state %v", dev, serial, st)
		}
	}
}

// TestReplayDeterminism 相同操作序列重放两次，结果必须逐字节一致。
func TestReplayDeterminism(t *testing.T) {
	run := func() []string {
		s := newSvc(t, 40, 100, 100)
		m := newSessMgr(s)
		type result struct {
			k   []string
			err error
		}
		var out []string
		emit := func(tag string) func([]string, error) {
			return func(k []string, err error) {
				out = append(out, fmt.Sprintf("%s k=%v err=%v", tag, kicks(k), err))
			}
		}
		emit("issue1")(s.Issue("d", "s1", 0, 1000, 0))
		emit("early")(s.Issue("d", "s2", 950, 2000, 899))
		emit("issue2")(s.Issue("d", "s2", 950, 2000, 900))
		emit("notyet")(m.Connect("d", "s2", 949))
		emit("confirm")(m.Connect("d", "s2", 950))
		emit("takeover")(m.Connect("d", "s1", 989))
		emit("retired")(m.Connect("d", "s1", 990))
		emit("settle")(s.Issue("z", "z1", 0, 10, 991))
		for _, snap := range s.Snapshot() {
			out = append(out, fmt.Sprintf("state %s=%s", snap.Cert.Serial, snap.St))
		}
		out = append(out, fmt.Sprintf("popped=%d", s.Popped()))
		return out
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("length %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("line %d:\n a=%s\n b=%s", i, a[i], b[i])
		}
	}
}

package backupretention

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	s := NewService()
	const n = 50
	base := mon20240101

	var wg sync.WaitGroup
	// 每个 goroutine 使用互不重叠的时间段，保证其内部单调；
	// 服务串行化后，成功登记的备份集合与其父子关系必须自洽。
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rootID := "root-" + itoa(g)
			t0 := base + int64(g)*1000
			_ = s.RegisterFull(rootID, 1, t0)
			for k := 1; k <= 5; k++ {
				child := rootID + "-" + itoa(k)
				_ = s.RegisterIncremental(child, 1, t0+int64(k), rootID)
			}
			_ = s.MarkCorrupt(rootID, t0+10)
			_ = s.SetLegalHold(rootID, t0+20, true)
		}(g)
	}
	wg.Wait()

	// 所有成功登记的增量其父必然存在（无数据竞争、无半成品写入）。
	for _, b := range s.reg.all() {
		if b.Kind == KindIncremental {
			if _, ok := s.reg.get(b.ParentID); !ok {
				t.Fatalf("incremental %q references missing parent after concurrent ops", b.ID)
			}
			if b.CreatedAt < s.reg.byID[b.ParentID].CreatedAt {
				t.Fatalf("invariant violated: child %q earlier than parent", b.ID)
			}
		}
	}

	// 并发只读计划不得 panic，结果确定。
	p1, err := s.Plan(base + n*1000)
	mustOK(t, err)
	var wg2 sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			_, _ = s.Plan(base + n*1000)
		}()
	}
	wg2.Wait()
	p2, err := s.Plan(base + n*1000)
	mustOK(t, err)
	if plansFingerprint(p1) != plansFingerprint(p2) {
		t.Fatal("concurrent plans must be deterministic")
	}
}

func TestLoggerRecordsInputsOutputs(t *testing.T) {
	var buf bytes.Buffer
	s := NewService(WithLogger(&buf))
	t0 := mon20240101
	mustOK(t, s.RegisterFull("OLD", 1, t0-10*daySeconds))
	mustOK(t, s.RegisterFull("F", 1, t0))
	mustOK(t, s.RegisterIncremental("I", 1, t0+10, "F"))
	mustOK(t, s.SetPolicy(Policy{Daily: 1}, t0+10))
	_, err := s.Plan(t0 + 10)
	mustOK(t, err)
	_ = s.MarkCorrupt("ghost", t0+5) // 时钟回退，记为 ERROR

	log := buf.String()
	for _, want := range []string{"RegisterFull", "RegisterIncremental", "SetPolicy", "Plan",
		"KEEP", "DELETE", "direct=[daily]", "ERROR kind=2"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

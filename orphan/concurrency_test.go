package orphan_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/orphan"
)

func concurrencyStore(t *testing.T) *orphan.Store {
	t.Helper()
	spec := orphan.RuleSpec{
		RequiredLinkTypes: map[orphan.ObjectTypeID][]orphan.LinkTypeID{"X": {"L"}},
		GracePeriod:       3,
	}
	s := orphan.NewStore(spec, 0)
	base := []orphan.Event{
		{ID: "lt", Kind: orphan.EvLinkTypeCreated, LinkType: "L", Time: 0},
		{ID: "o", Kind: orphan.EvObjectCreated, Object: "o", ObjectType: "X", Time: 1},
		{ID: "p", Kind: orphan.EvObjectCreated, Object: "p", ObjectType: "Y", Time: 1},
		{ID: "l", Kind: orphan.EvLinkCreated, Link: "k", LinkType: "L", From: "o", To: "p", Time: 2},
		{ID: "r", Kind: orphan.EvLinkRevoked, Link: "k", Time: 4},
	}
	if err := s.Append(base...); err != nil {
		t.Fatal(err)
	}
	return s
}

// 固定数据上并发判定：所有结果必须与串行结果完全一致（幂等）。
func TestConcurrentDeterminesAreIdentical(t *testing.T) {
	s := concurrencyStore(t)
	want, err := s.Determine(orphan.Query{Object: "o", At: 10, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				got, err := s.Determine(orphan.Query{Object: "o", At: 10, Version: 1})
				if err != nil {
					t.Error(err)
					return
				}
				if got.Status != want.Status || got.OrphanDue != want.OrphanDue || got.ZeroSince != want.ZeroSince {
					t.Errorf("concurrent result differs: %+v vs %+v", got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// 事件追加、规则调整与判定并发发起：最终可观察结果必须等价于
// 某个全局串行顺序。验证方式：每条审计记录声明的版本，必须等于
// 该记录线性化序号处账本实际治理的版本（否则必须报 RuleVersionVoided）。
func TestConcurrentMixedOperationsLinearizable(t *testing.T) {
	s := concurrencyStore(t)

	var determines atomic.Int64
	var wg sync.WaitGroup

	// 事件追加者。
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = s.Append(orphan.Event{
					ID:     orphan.EventID(fmt.Sprintf("g%d-%d", g, i)),
					Kind:   orphan.EvPropertySet,
					Object: "p", Key: "k", Value: "v",
					Time: orphan.Time(5 + i),
				})
			}
		}(g)
	}

	// 规则调整者（EffectiveFrom 由原子计数保证递增；失败可忽略）。
	var adjustClock atomic.Int64
	adjustClock.Store(10)
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				at := orphan.Time(adjustClock.Add(1))
				spec := orphan.RuleSpec{
					RequiredLinkTypes: map[orphan.ObjectTypeID][]orphan.LinkTypeID{"X": {"L"}},
					GracePeriod:       orphan.Time(i % 4),
				}
				_, _ = s.AdjustRule(spec, i%2 == 0, at)
			}
		}(g)
	}

	// 判定者：声明版本取当时的治理版本（可能因并发调整而过期 → E1，属合法结果）。
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				ledger := s.RuleLedger()
				at := orphan.Time(i % 12)
				gov, ok := orphan.GoverningVersion(ledger, at)
				declared := 1
				if ok {
					declared = gov.Version
				}
				_, _ = s.Determine(orphan.Query{Object: "o", At: at, Version: declared})
				determines.Add(1)
			}
		}()
	}
	wg.Wait()

	// 每次判定都必须被审计。
	log := s.AuditLog()
	if int64(len(log)) != determines.Load() {
		t.Fatalf("audit entries %d, want %d", len(log), determines.Load())
	}

	// 线性化点校验：用最终账本中 CreateSeq <= rec.Seq 的版本重建当时治理关系。
	ledger := s.RuleLedger()
	for _, rec := range log {
		var visible []orphan.RuleVersion
		for _, v := range ledger {
			if v.CreateSeq <= rec.Seq {
				visible = append(visible, v)
			}
		}
		gov, ok := orphan.GoverningVersion(visible, rec.Query.At)
		voided := !ok || gov.Version != rec.Declared
		if voided {
			if rec.Err != orphan.ErrRuleVersionVoided {
				t.Fatalf("rec %+v: expected RuleVersionVoided at seq %d", rec, rec.Seq)
			}
			continue
		}
		if rec.Err == orphan.ErrRuleVersionVoided {
			t.Fatalf("rec %+v: version %d was governing at seq %d", rec, rec.Declared, rec.Seq)
		}
		if rec.Governing != gov.Version {
			t.Fatalf("rec %+v: governing mismatch, want %d", rec, gov.Version)
		}
	}
}

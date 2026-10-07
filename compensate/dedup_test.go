package compensate

import (
	"strings"
	"sync/atomic"
	"testing"
)

// 边界穷举：重复投递（同 EventID）与独立等价调用（不同 EventID、内容等价）
// 的全部 7 种判定边界。
func TestDedupBoundaryExhaustive(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, p *Processor, store *MemoryStore)
	}{
		{
			name: "1_first_delivery_runs_compensation_once",
			run: func(t *testing.T, p *Processor, store *MemoryStore) {
				createTarget(store, "o1")
				cnt := make([]atomic.Int32, 2)
				reg := NewRegistry()
				_ = multiSpec(reg, "A", 2, "biz/o1", cnt)
				p2 := NewProcessor(Config{Store: store, Specs: reg})
				r := mustHandle(p2, testEvent("e1", "A", map[string]string{"object": "o1"}))
				if r.Outcome != OutcomeCompleted || r.Resumed {
					t.Fatalf("want fresh COMPLETED, got %+v", r)
				}
				if totalOcc(committedBusinessOccurrences(store, "biz/o1")) != 2 {
					t.Fatalf("want 2 effect occurrences, got %v",
						committedBusinessOccurrences(store, "biz/o1"))
				}
			},
		},
		{
			name: "2_identical_redelivery_is_skipped_no_new_side_effects",
			run: func(t *testing.T, p *Processor, store *MemoryStore) {
				createTarget(store, "o2")
				cnt := make([]atomic.Int32, 2)
				reg := NewRegistry()
				_ = multiSpec(reg, "A", 2, "biz/o2", cnt)
				p2 := NewProcessor(Config{Store: store, Specs: reg})
				ev := testEvent("e2", "A", map[string]string{"object": "o2"})
				_ = mustHandle(p2, ev)
				r := mustHandle(p2, ev) // 网络重试：同一 EventID 完全相同
				if r.Outcome != OutcomeCompleted {
					t.Fatalf("duplicate should return completed idempotently, got %+v", r)
				}
				if totalOcc(committedBusinessOccurrences(store, "biz/o2")) != 2 {
					t.Fatalf("redelivery changed effects: %v",
						committedBusinessOccurrences(store, "biz/o2"))
				}
			},
		},
		{
			name: "3_independent_equivalent_call_gets_own_compensation",
			run: func(t *testing.T, p *Processor, store *MemoryStore) {
				createTarget(store, "o3")
				cnt := make([]atomic.Int32, 2)
				reg := NewRegistry()
				_ = multiSpec(reg, "A", 2, "biz/o3", cnt)
				p2 := NewProcessor(Config{Store: store, Specs: reg})
				ev1 := testEvent("e3a", "A", map[string]string{"object": "o3"})
				// 极短时间内的二次调用：参数/效果完全等价，但生产者分配了新 EventID。
				ev2 := testEvent("e3b", "A", map[string]string{"object": "o3"})
				_ = mustHandle(p2, ev1)
				r := mustHandle(p2, ev2)
				if r.Outcome != OutcomeCompleted {
					t.Fatalf("independent equivalent call must compensate, got %+v", r)
				}
				if totalOcc(committedBusinessOccurrences(store, "biz/o3")) != 4 {
					t.Fatalf("two real calls want 4 occurrences, got %v",
						committedBusinessOccurrences(store, "biz/o3"))
				}
			},
		},
		{
			name: "4_redelivery_while_superseded_is_skipped",
			run: func(t *testing.T, _ *Processor, store *MemoryStore) {
				reg := NewRegistry()
				cnt := make([]atomic.Int32, 1)
				_ = multiSpec(reg, "A", 1, "biz/o4", cnt)
				p2 := NewProcessor(Config{Store: store, Specs: reg})
				ev := testEvent("e4", "A", map[string]string{"object": "o4"})
				if got := p2.UndoArrived(ev); got != UndoWins {
					t.Fatalf("undo before claim must win, got %s", got)
				}
				r := mustHandle(p2, ev)
				if r.Outcome != OutcomeSuperseded {
					t.Fatalf("want SUPERSEDED, got %+v", r)
				}
				if r2 := mustHandle(p2, ev); r2.Outcome != OutcomeSuperseded {
					t.Fatalf("redelivery must stay superseded, got %+v", r2)
				}
				if totalOcc(committedBusinessOccurrences(store, "biz/o4")) != 0 {
					t.Fatalf("superseded must have zero effects, got %v",
						committedBusinessOccurrences(store, "biz/o4"))
				}
			},
		},
		{
			name: "5_undo_after_claim_too_late_compensation_finishes",
			run: func(t *testing.T, _ *Processor, store *MemoryStore) {
				createTarget(store, "o5")
				reg := NewRegistry()
				cnt := make([]atomic.Int32, 2)
				_ = multiSpec(reg, "A", 2, "biz/o5", cnt)
				p2 := NewProcessor(Config{Store: store, Specs: reg})
				ev := testEvent("e5", "A", map[string]string{"object": "o5"})
				_ = mustHandle(p2, ev)
				if got := p2.UndoArrived(ev); got != UndoTooLate {
					t.Fatalf("undo after claim must be too late, got %s", got)
				}
				if r := mustHandle(p2, ev); r.Outcome != OutcomeCompleted {
					t.Fatalf("compensation stays complete, got %+v", r)
				}
				if totalOcc(committedBusinessOccurrences(store, "biz/o5")) != 2 {
					t.Fatalf("effects must remain applied, got %v",
						committedBusinessOccurrences(store, "biz/o5"))
				}
			},
		},
		{
			name: "6_same_id_different_content_is_E3_not_guessed",
			run: func(t *testing.T, _ *Processor, store *MemoryStore) {
				createTarget(store, "o6")
				reg := NewRegistry()
				cnt := make([]atomic.Int32, 1)
				_ = multiSpec(reg, "A", 1, "biz/o6", cnt)
				p2 := NewProcessor(Config{Store: store, Specs: reg})
				_ = mustHandle(p2, testEvent("e6", "A", map[string]string{"object": "o6"}))
				r := mustHandle(p2, testEvent("e6", "A", map[string]string{"object": "DIFFERENT"}))
				if r.Err == nil || r.Err.Class != ErrAmbiguousIdentity {
					t.Fatalf("want E3 ambiguous identity, got %+v", r)
				}
				if totalOcc(committedBusinessOccurrences(store, "biz/o6")) != 1 {
					t.Fatalf("E3 must add no effects, got %v",
						committedBusinessOccurrences(store, "biz/o6"))
				}
			},
		},
		{
			name: "7_empty_identity_is_E3",
			run: func(t *testing.T, _ *Processor, store *MemoryStore) {
				reg := NewRegistry()
				p2 := NewProcessor(Config{Store: store, Specs: reg})
				r := mustHandle(p2, testEvent("", "A", nil))
				if r.Err == nil || r.Err.Class != ErrAmbiguousIdentity {
					t.Fatalf("want E3 for empty EventID, got %+v", r)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			p := NewProcessor(Config{Store: store, Specs: NewRegistry()})
			tc.run(t, p, store)
		})
	}
}

// 每次判定的输入 EventID 与对照结论必须可在审计中逐条核查。
func TestDedupDecisionsAreAudited(t *testing.T) {
	store := NewMemoryStore()
	reg := NewRegistry()
	cnt := make([]atomic.Int32, 1)
	_ = multiSpec(reg, "A", 1, "biz/audit", cnt)
	p := NewProcessor(Config{Store: store, Specs: reg})
	createTarget(store, "audit")
	ev := testEvent("audit-1", "A", map[string]string{"object": "audit"})
	_ = mustHandle(p, ev)
	_ = mustHandle(p, ev)

	recs := (AuditLog{}).Snapshot(store)
	var conclusions []string
	for _, r := range recs {
		if r.Kind == AuditDedup {
			if r.EventID != "audit-1" {
				t.Fatalf("audit must record input EventID, got %q", r.EventID)
			}
			conclusions = append(conclusions, r.Detail["conclusion"])
			if r.Detail["probes"] != "1" {
				t.Fatalf("dedup must be exactly 1 indexed probe, got %q", r.Detail["probes"])
			}
		}
	}
	if len(conclusions) < 2 || conclusions[0] != "NEW_EVENT" || conclusions[1] != "DUPLICATE_DELIVERY_SKIP" {
		t.Fatalf("audit conclusions wrong: %v", conclusions)
	}
	if !strings.Contains(conclusions[0]+conclusions[1], "DUPLICATE") {
		t.Fatalf("duplicate conclusion missing: %v", conclusions)
	}
}

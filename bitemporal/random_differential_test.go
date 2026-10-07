package bitemporal

import (
	"context"
	"math/rand"
	"testing"
)

// TestRandomDifferential 用随机操作序列逐条对照索引实现与朴素实现：
// 每次写入后在随机双时态点比较回放结果；在多个记录时间点比较对角线审计结论。
func TestRandomDifferential(t *testing.T) {
	seeds := []int64{1, 2, 3, 7, 42, 99, 2024}
	for _, seed := range seeds {
		seed := seed
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			runDifferential(t, rng, false)
		})
	}
}

func TestRandomDifferentialSymmetric(t *testing.T) {
	seeds := []int64{11, 22, 33, 44}
	for _, seed := range seeds {
		seed := seed
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			runDifferential(t, rng, true)
		})
	}
}

func runDifferential(t *testing.T, rng *rand.Rand, symmetric bool) {
	t.Helper()
	st := NewStore()
	log := NewDecisionLog()
	aud := NewAuditor(st, log)
	must(t, st.RegisterObjectType("T", 0))
	if !symmetric {
		must(t, st.RegisterObjectType("U", 0))
	}
	lt := LinkType{ID: "L", SourceType: "T", TargetType: "U"}
	if symmetric {
		lt = LinkType{ID: "L", SourceType: "T", TargetType: "T", Symmetric: true}
	}
	// 随机基数，制造违反/不违反两种历史。
	max := rng.Intn(4)
	must(t, st.RegisterLinkType(lt,
		Cardinality{Forward: Card{Max: max}, Reverse: Card{Max: max}}, 0))

	objects := []ID{"a", "b", "c", "d"}
	pick := func() ID { return objects[rng.Intn(len(objects))] }

	// 规则版本切换的时间点，保证严格递增。
	nextRuleTick := int64(25)

	ops := 200
	for op := 0; op < ops; op++ {
		tick := int64(op + 1)
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4:
			a, b := pick(), pick()
			for a == b && !symmetric {
				b = pick()
			}
			if a == b {
				// 对称链接自环不在审计模型内，跳过。
				continue
			}
			vt := int64(1 + rng.Intn(int(tick)+1))
			if rng.Intn(5) == 0 {
				_ = st.IngestLegacyHalf("L", a, b, vt, tick, true,
					randomSide(rng, symmetric))
			} else {
				_ = st.RecordCreate("L", a, b, vt, tick)
			}
		case 5, 6:
			a, b := pick(), pick()
			for a == b && !symmetric {
				b = pick()
			}
			if a == b {
				continue
			}
			vt := int64(1 + rng.Intn(int(tick)+1))
			if rng.Intn(5) == 0 {
				_ = st.IngestLegacyHalf("L", a, b, vt, tick, false,
					randomSide(rng, symmetric))
			} else {
				_ = st.RecordRevoke("L", a, b, vt, tick)
			}
		case 7:
			if tick > nextRuleTick {
				m := rng.Intn(5)
				_ = st.PutRule("L",
					Cardinality{Forward: Card{Max: m}, Reverse: Card{Max: m}}, tick)
				nextRuleTick = tick + int64(10+rng.Intn(20))
			}
		}

		// 每个操作后随机取若干双时态点对照回放。
		snap := st.CurrentSnapshot()
		for q := 0; q < 3; q++ {
			rt := int64(rng.Intn(int(tick) + 2))
			vt := int64(rng.Intn(int(tick) + 2))
			got := snap.Replay("L", rt, vt)
			want := snap.NaiveReplay("L", rt, vt)
			if !replayEqual(got, want) {
				t.Fatalf("seed replay mismatch op=%d rt=%d vt=%d\n got=%+v\nwant=%+v",
					op, rt, vt, got, want)
			}
		}

		// 对角线审计：跑一个窗口，并与朴素逐点结论比较版本与违反集合。
		if op%10 == 9 {
			start := int64(rng.Intn(int(tick) + 1))
			end := start + int64(1+rng.Intn(20))
			segs, err := aud.Audit(context.Background(), AuditRequest{
				LinkType: "L", RecordStart: start, RecordEnd: end})
			if err != nil {
				// E4 是合法结果：朴素模型在窗口内同样必须能发现结构缺陷。
				if ae, ok := err.(*AuditError); ok && ae.Kind == ErrMirrorStructural {
					found := false
					for tt := start; tt < end; tt++ {
						if _, _, defects := snap.NaiveAudit("L", tt); len(defects) > 0 {
							found = true
						}
					}
					if !found {
						t.Fatalf("false structural error at op=%d window=[%d,%d)", op, start, end)
					}
					continue
				}
				t.Fatalf("unexpected audit error op=%d: %v", op, err)
			}
			for _, seg := range segs {
				for tt := seg.RecordStart; tt < seg.RecordEnd; tt++ {
					rule, wantViol, defects := snap.NaiveAudit("L", tt)
					if len(defects) > 0 {
						t.Fatalf("naive found defect but audit did not at %d", tt)
					}
					if rule.FromRecord != seg.Rule.FromRecord {
						t.Fatalf("rule version mismatch at %d: %d vs %d",
							tt, rule.FromRecord, seg.Rule.FromRecord)
					}
					if !violSetEqual(wantViol, seg.Violations) {
						t.Fatalf("violations mismatch at %d:\n got=%+v\nwant=%+v",
							tt, seg.Violations, wantViol)
					}
				}
			}
		}
	}

	// 每次审计都必须留痕，且留痕包含输入、版本代号与结论。
	for _, rec := range log.Records() {
		if rec.Request.LinkType != "L" {
			t.Fatalf("decision record missing request: %+v", rec)
		}
		if rec.SnapshotGen == 0 && rec.Err == "" {
			t.Fatalf("decision record missing basis: %+v", rec)
		}
	}
}

func randomSide(rng *rand.Rand, symmetric bool) string {
	if symmetric {
		if rng.Intn(2) == 0 {
			return halfAB
		}
		return halfBA
	}
	if rng.Intn(2) == 0 {
		return halfF
	}
	return halfR
}

func violSetEqual(a, b []Violation) bool {
	key := func(v Violation) string {
		return v.Direction + "\x00" + string(v.Object)
	}
	if len(a) != len(b) {
		return false
	}
	ma := map[string]Violation{}
	for _, v := range a {
		ma[key(v)] = v
	}
	for _, v := range b {
		x, ok := ma[key(v)]
		if !ok || x.Outgoing != v.Outgoing || x.Card != v.Card {
			return false
		}
	}
	return true
}

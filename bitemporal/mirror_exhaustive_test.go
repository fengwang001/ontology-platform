package bitemporal

import (
	"fmt"
	"testing"
)

// halfSpec 描述一个物理半在若干双时态点上的事件序列（按时间排序后写入）。
// 每个元素：(valid, record, create=true/false, side)。
type halfEvent struct {
	vt, rt int64
	create bool
	side   string
}

// mirrorScenario 枚举两个物理半各自的事件集合。
type mirrorScenario struct {
	name string
	sym  bool
	a    []halfEvent
	b    []halfEvent
}

// allHalfHistories 穷举一个物理半的“创建/撤销在两个双时态点上”的边界历史。
// 只取小而全的代表集：
//  1. 空
//  2. 仅创建 (v=10,r=10)
//  3. 创建后撤销（撤销事实晚知晓 r=40,v=30）
//  4. 创建后撤销（撤销立刻知晓 r=30,v=30）
//  5. 迟到创建（先有 r=40 的事实，创建事实 r=20 晚到不可能——record 只增；
//     改为：晚知晓的“补登创建” v=5,r=5,r=20，即更早有效的事实更晚被记录）
func allHalfHistories(side string) [][]halfEvent {
	return [][]halfEvent{
		nil,
		{{10, 10, true, side}},
		{{10, 10, true, side}, {30, 40, false, side}},
		{{10, 10, true, side}, {30, 30, false, side}},
		{{5, 20, true, side}, {10, 10, true, side}},
		{{10, 10, true, side}, {30, 40, false, side}, {40, 40, true, side}},
	}
}

func buildMirrorStore(t *testing.T, sym bool, evA, evB []halfEvent) (*Store, ID) {
	t.Helper()
	st := NewStore()
	must(t, st.RegisterObjectType("T", 0))
	if !sym {
		must(t, st.RegisterObjectType("U", 0))
	}
	lt := LinkType{ID: "L", SourceType: "T", TargetType: "U"}
	if sym {
		lt = LinkType{ID: "L", SourceType: "T", TargetType: "T", Symmetric: true}
	}
	must(t, st.RegisterLinkType(lt, Cardinality{Forward: Card{Max: 0}, Reverse: Card{Max: 0}}, 0))

	var evs []halfEvent
	evs = append(evs, evA...)
	evs = append(evs, evB...)
	sortEvs(evs)
	for _, e := range evs {
		err := st.IngestLegacyHalf("L", "a", "b", e.vt, e.rt, e.create, e.side)
		must(t, err)
	}
	return st, "L"
}

var probePoints = []struct{ rt, vt int64 }{
	{0, 0}, {5, 5}, {9, 9}, {10, 10}, {15, 10}, {15, 15},
	{20, 5}, {20, 10}, {20, 20}, {29, 29}, {30, 20}, {35, 20},
	{35, 35}, {40, 30}, {40, 40}, {50, 50},
}

func TestMirrorExhaustive(t *testing.T) {
	for _, sym := range []bool{false, true} {
		var sideA, sideB string
		if sym {
			sideA, sideB = halfAB, halfBA
		} else {
			sideA, sideB = halfF, halfR
		}
		histA := allHalfHistories(sideA)
		histB := allHalfHistories(sideB)
		for i, ha := range histA {
			for j, hb := range histB {
				st, lt := buildMirrorStore(t, sym, ha, hb)
				snap := st.CurrentSnapshot()
				for _, pp := range probePoints {
					got := snap.Replay(lt, pp.rt, pp.vt)
					want := snap.NaiveReplay(lt, pp.rt, pp.vt)
					if !replayEqual(got, want) {
						t.Logf("evA=%+v evB=%+v facts=%+v", ha, hb, snap.facts[lt])
						t.Fatalf("sym=%v hist=(%d,%d) point=(%d,%d)\n got=%+v\nwant=%+v",
							sym, i, j, pp.rt, pp.vt, got, want)
					}
					fwd, rev, defects := snap.CheckMirror(lt, pp.rt, pp.vt)
					if len(defects) != len(want.Defects) {
						t.Fatalf("mirror defects mismatch sym=%v (%d,%d) (%d,%d): %d vs %d",
							sym, i, j, pp.rt, pp.vt, len(defects), len(want.Defects))
					}
					if !isMirror(fwd, rev) {
						t.Fatalf("forward/reverse not mirrors: %v vs %v", fwd, rev)
					}
				}
			}
		}
	}
}

func replayEqual(a, b ReplayResult) bool {
	if len(a.Edges) != len(b.Edges) || len(a.Defects) != len(b.Defects) {
		return false
	}
	for i := range a.Edges {
		if a.Edges[i] != b.Edges[i] {
			return false
		}
	}
	for i := range a.Defects {
		x, y := a.Defects[i], b.Defects[i]
		if x.A != y.A || x.B != y.B || x.MissingSide != y.MissingSide {
			return false
		}
	}
	return true
}

func isMirror(fwd, rev []Edge) bool {
	if len(fwd) != len(rev) {
		return false
	}
	m := edgeSet(fwd)
	for _, e := range rev {
		if !m[[2]ID{e.To, e.From}] {
			return false
		}
	}
	return true
}

// TestSymmetricSingleSideDefects 明确覆盖“对称链接仅一端有记录”的每一个时刻。
func TestSymmetricSingleSideDefects(t *testing.T) {
	for _, side := range []string{halfAB, halfBA} {
		t.Run(fmt.Sprintf("side=%s", side), func(t *testing.T) {
			st := NewStore()
			must(t, st.RegisterObjectType("T", 0))
			must(t, st.RegisterLinkType(
				LinkType{ID: "L", SourceType: "T", TargetType: "T", Symmetric: true},
				Cardinality{Forward: Card{Max: 1}, Reverse: Card{Max: 1}}, 0))
			must(t, st.IngestLegacyHalf("L", "a", "b", 10, 10, true, side))
			snap := st.CurrentSnapshot()
			for rt := int64(0); rt <= 30; rt++ {
				for vt := int64(0); vt <= 30; vt++ {
					r := snap.Replay("L", rt, vt)
					known := rt >= 10 && vt >= 10
					if known {
						if len(r.Edges) != 0 || len(r.Defects) != 1 {
							t.Fatalf("rt=%d vt=%d expected single defect, got edges=%v defects=%v",
								rt, vt, r.Edges, r.Defects)
						}
						missing := halfAB
						if side == halfAB {
							missing = halfBA
						}
						if r.Defects[0].MissingSide != missing {
							t.Fatalf("rt=%d vt=%d missing=%s want %s",
								rt, vt, r.Defects[0].MissingSide, missing)
						}
					} else if len(r.Edges) != 0 || len(r.Defects) != 0 {
						t.Fatalf("rt=%d vt=%d expected empty, got %+v", rt, vt, r)
					}
				}
			}
		})
	}
}

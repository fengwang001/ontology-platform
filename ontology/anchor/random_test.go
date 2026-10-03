package anchor

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestRandomAgainstCharacterAttachmentModel(t *testing.T) {
	for trial := 0; trial < 2000; trial++ {
		rng := rand.New(rand.NewSource(int64(trial + 1)))
		initialLength := rng.Intn(16)
		maxLength := initialLength + rng.Intn(21)
		maxAnchors := 1 + rng.Intn(8)
		tr := New(initialLength, maxLength, maxAnchors)
		model := newNaive(initialLength)
		var log strings.Builder
		fmt.Fprintf(&log, "TRIAL %d New(N0=%d,MaxLen=%d,MaxAnchors=%d)", trial, initialLength, maxLength, maxAnchors)

		compareAll := func(stage string, floor int) {
			for id, a := range model.anchors {
				removed := model.removed[id]
				for rev := 0; rev <= model.rev; rev++ {
					if removed {
						if _, err := tr.Pos(id, rev); err != ErrNoAnchor {
							t.Fatalf("%s | %s: removed Pos id=%d rev=%d output=%v basis=ErrNoAnchor priority", log.String(), stage, id, rev, err)
						}
						if _, _, _, err := tr.Range(id, rev); err != ErrNoAnchor {
							t.Fatalf("%s | %s: removed Range id=%d rev=%d output=%v basis=ErrNoAnchor priority", log.String(), stage, id, rev, err)
						}
						continue
					}

					if rev < a.createdAt {
						if a.kind == pointAnchor {
							if _, err := tr.Pos(id, rev); err != ErrNotYet {
								t.Fatalf("%s | %s: early Pos id=%d rev=%d output=%v basis=ErrNotYet", log.String(), stage, id, rev, err)
							}
						} else if _, _, _, err := tr.Range(id, rev); err != ErrNotYet {
							t.Fatalf("%s | %s: early Range id=%d rev=%d output=%v basis=ErrNotYet", log.String(), stage, id, rev, err)
						}
						continue
					}
					if rev < floor {
						if a.kind == pointAnchor {
							if _, err := tr.Pos(id, rev); err != ErrCompacted {
								t.Fatalf("%s | %s: compacted Pos id=%d rev=%d output=%v basis=ErrCompacted boundary", log.String(), stage, id, rev, err)
							}
						} else if _, _, _, err := tr.Range(id, rev); err != ErrCompacted {
							t.Fatalf("%s | %s: compacted Range id=%d rev=%d output=%v basis=ErrCompacted boundary", log.String(), stage, id, rev, err)
						}
						continue
					}

					if a.kind == pointAnchor {
						want := model.point(id, rev)
						got, err := tr.Pos(id, rev)
						if err != nil || got != want {
							t.Fatalf("%s | %s: Pos id=%d rev=%d output=(%d,%v) want=%d basis=character/BOS/EOS attachment", log.String(), stage, id, rev, got, err, want)
						}
						if _, _, _, err := tr.Range(id, rev); err != ErrWrongKind {
							t.Fatalf("%s | %s: Range(point id=%d) output=%v basis=ErrWrongKind", log.String(), stage, id, err)
						}
					} else {
						wantStart, wantEnd, wantCollapsed := model.rangeResult(id, rev)
						gotStart, gotEnd, gotCollapsed, err := tr.Range(id, rev)
						if err != nil || gotStart != wantStart || gotEnd != wantEnd || gotCollapsed != wantCollapsed {
							t.Fatalf("%s | %s: Range id=%d rev=%d output=(%d,%d,%v,%v) want=(%d,%d,%v) basis=biased endpoints and collapse",
								log.String(), stage, id, rev, gotStart, gotEnd, gotCollapsed, err, wantStart, wantEnd, wantCollapsed)
						}
						if _, err := tr.Pos(id, rev); err != ErrWrongKind {
							t.Fatalf("%s | %s: Pos(range id=%d) output=%v basis=ErrWrongKind", log.String(), stage, id, err)
						}
					}
				}

				if !removed {
					if a.kind == pointAnchor {
						if _, err := tr.Pos(id, model.rev+1); err != ErrFuture {
							t.Fatalf("%s | %s: future Pos id=%d output=%v basis=ErrFuture after kind check", log.String(), stage, id, err)
						}
						if _, _, _, err := tr.Range(id, model.rev+1); err != ErrWrongKind {
							t.Fatalf("%s | %s: future Range(point id=%d) output=%v basis=ErrWrongKind precedence", log.String(), stage, id, err)
						}
					} else {
						if _, _, _, err := tr.Range(id, model.rev+1); err != ErrFuture {
							t.Fatalf("%s | %s: future Range id=%d output=%v basis=ErrFuture after kind check", log.String(), stage, id, err)
						}
						if _, err := tr.Pos(id, model.rev+1); err != ErrWrongKind {
							t.Fatalf("%s | %s: future Pos(range id=%d) output=%v basis=ErrWrongKind precedence", log.String(), stage, id, err)
						}
					}
				}
			}
		}

		for step := 0; step < 14; step++ {
			switch rng.Intn(8) {
			case 0, 1:
				position := rng.Intn(tr.Len() + 2)
				deleted := rng.Intn(6)
				inserted := rng.Intn(6)
				beforeLength := tr.Len()
				fmt.Fprintf(&log, " | Replace(%d,%d,%d)", position, deleted, inserted)
				rev, err := tr.Replace(position, deleted, inserted)
				fmt.Fprintf(&log, "->(%d,%v)", rev, err)
				validArguments := position >= 0 && deleted >= 0 && inserted >= 0 && deleted+inserted > 0 &&
					position+deleted <= beforeLength
				if !validArguments {
					if err != ErrInvalid {
						t.Fatalf("%s | output=%v basis=ErrInvalid precedes ErrTooLarge", log.String(), err)
					}
					continue
				}
				if beforeLength-deleted+inserted > maxLength {
					if err != ErrTooLarge {
						t.Fatalf("%s | output=%v basis=MaxLen rejection", log.String(), err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s | unexpected output=%v", log.String(), err)
				}
				model.replace(position, deleted, inserted)
			case 2:
				position := rng.Intn(tr.Len() + 2)
				length := 1 + rng.Intn(6)
				destination := rng.Intn(tr.Len() + 2)
				fmt.Fprintf(&log, " | Move(%d,%d,%d)", position, length, destination)
				rev, err := tr.Move(position, length, destination)
				fmt.Fprintf(&log, "->(%d,%v)", rev, err)
				valid := length >= 1 && position >= 0 && position+length <= tr.Len() &&
					destination >= 0 && destination <= tr.Len() &&
					(destination < position || destination > position+length)
				if !valid {
					if err != ErrInvalid {
						t.Fatalf("%s | output=%v basis=invalid move gap or bounds", log.String(), err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s | unexpected output=%v", log.String(), err)
				}
				model.move(position, length, destination)
			case 3:
				position := rng.Intn(tr.Len() + 2)
				bias := Bias(rng.Intn(2))
				fmt.Fprintf(&log, " | AddPoint(%d,%d)", position, bias)
				id, err := tr.AddPoint(position, bias)
				fmt.Fprintf(&log, "->(%d,%v)", id, err)
				if position < 0 || position > tr.Len() {
					if err != ErrOutOfRange {
						t.Fatalf("%s | output=%v basis=point bounds", log.String(), err)
					}
				} else if model.live >= maxAnchors {
					if err != ErrTooMany {
						t.Fatalf("%s | output=%v basis=MaxAnchors", log.String(), err)
					}
				} else if err != nil || id != model.addPoint(position, bias) {
					t.Fatalf("%s | id/output=(%d,%v) basis=incremental non-reused IDs", log.String(), id, err)
				}
			case 4:
				start := rng.Intn(tr.Len() + 2)
				end := start + rng.Intn(5) - 1
				if rng.Intn(3) == 0 {
					end = rng.Intn(tr.Len() + 2)
				}
				kind := RangeKind(rng.Intn(2))
				fmt.Fprintf(&log, " | AddRange(%d,%d,%d)", start, end, kind)
				id, err := tr.AddRange(start, end, kind)
				fmt.Fprintf(&log, "->(%d,%v)", id, err)
				if start < 0 || end < 0 || start > tr.Len() || end > tr.Len() || start > end {
					if err != ErrOutOfRange {
						t.Fatalf("%s | output=%v basis=range bounds", log.String(), err)
					}
				} else if model.live >= maxAnchors {
					if err != ErrTooMany {
						t.Fatalf("%s | output=%v basis=MaxAnchors", log.String(), err)
					}
				} else if err != nil || id != model.addRange(start, end, kind) {
					t.Fatalf("%s | id/output=(%d,%v) basis=incremental non-reused IDs", log.String(), id, err)
				}
			case 5:
				if model.live == 0 {
					break
				}
				candidates := make([]int, 0, len(model.anchors))
				for id := range model.anchors {
					if !model.removed[id] {
						candidates = append(candidates, id)
					}
				}
				id := candidates[rng.Intn(len(candidates))]
				fmt.Fprintf(&log, " | Remove(%d)", id)
				err := tr.Remove(id)
				fmt.Fprintf(&log, "->(%v)", err)
				if err != nil {
					t.Fatalf("%s | unexpected output=%v", log.String(), err)
				}
				model.remove(id)
			case 6:
				if tr.Rev() <= tr.Floor() {
					break
				}
				targets := []int{tr.Floor() - 1, tr.Floor(), tr.Floor() + 1, tr.Rev(), tr.Rev() + 1}
				newFloor := targets[rng.Intn(len(targets))]
				wantReplayed := 0
				if newFloor >= tr.Floor() && newFloor <= tr.Rev() {
					for id, a := range tr.anchors {
						if !a.removed && a.floorRev < newFloor && !model.removed[id] {
							wantReplayed += newFloor - a.floorRev
						}
					}
				}
				fmt.Fprintf(&log, " | Compact(%d)", newFloor)
				err := tr.Compact(newFloor)
				fmt.Fprintf(&log, "->(%v)", err)
				if newFloor < tr.Floor() || newFloor > tr.Rev() {
					if err != ErrBadFloor {
						t.Fatalf("%s | output=%v basis=ErrBadFloor", log.String(), err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s | unexpected output=%v", log.String(), err)
				}
				if tr.replayed != wantReplayed {
					t.Fatalf("%s | replay output=%d want=%d basis=materialized anchor checkpoint deltas", log.String(), tr.replayed, wantReplayed)
				}
				compareAll("after compact", newFloor)
			default:
				compareAll(fmt.Sprintf("step %d", step), tr.Floor())
			}
		}

		compareAll("final", tr.Floor())
		fmt.Fprintf(&log, " | verdict=PASS basis=all point/range outputs match naive character attachment and rejection rules")
		t.Log(log.String())
	}
}

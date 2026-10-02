package compaction

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naivePicker 是按规则逐条直译的朴素参考实现：不做任何优化，
// 每步都整体排序、线性扫描，用于与 Picker 的随机对拍。
type naivePicker struct {
	L     int
	caps  []int64 // 下标 1..L-1 有效
	X     int64
	files map[uint64]File
	ptr   map[int][]byte
}

func newNaive(L int, caps []int64, X int64) *naivePicker {
	c := make([]int64, L+1)
	copy(c[1:], caps)
	return &naivePicker{L: L, caps: c, X: X, files: map[uint64]File{}, ptr: map[int][]byte{}}
}

func (n *naivePicker) levelFiles(level int) []File {
	var out []File
	for _, file := range n.files {
		if file.Level == level {
			out = append(out, file)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].Min, out[j].Min) < 0
	})
	return out
}

func naiveOverlap(aMin, aMax, bMin, bMax []byte) bool {
	return bytes.Compare(aMin, bMax) <= 0 && bytes.Compare(bMin, aMax) <= 0
}

func (n *naivePicker) addFile(file File) error {
	if file.Level < 1 || file.Level > n.L {
		return ErrLevelOutOfRange
	}
	if bytes.Compare(file.Min, file.Max) > 0 {
		return ErrBadRange
	}
	if file.Size <= 0 {
		return ErrNonPositiveSize
	}
	if _, dup := n.files[file.ID]; dup {
		return ErrDuplicateID
	}
	for _, g := range n.files {
		if g.Level == file.Level && naiveOverlap(g.Min, g.Max, file.Min, file.Max) {
			return ErrOverlap
		}
	}
	n.files[file.ID] = file
	return nil
}

// pick 执行一次朴素选择，并返回判定依据日志。
func (n *naivePicker) pick() (inputs, overlaps []File, err error, log string) {
	// ① 选层：用有理数比较得分，并列时先遇到的（层号小者）胜出。
	best, bestScore := -1, new(big.Rat)
	scores := map[int]string{}
	for i := 1; i <= n.L-1; i++ {
		var total int64
		for _, file := range n.files {
			if file.Level == i {
				total += file.Size
			}
		}
		score := new(big.Rat).SetFrac(big.NewInt(total), big.NewInt(n.caps[i]))
		scores[i] = fmt.Sprintf("%d/%d", total, n.caps[i])
		if best == -1 || score.Cmp(bestScore) > 0 {
			best, bestScore = i, score
		}
	}
	log += fmt.Sprintf("scores=%v best=L%d", scores, best)
	if bestScore.Cmp(big.NewRat(1, 1)) < 0 {
		return nil, nil, ErrNothingToCompact, log + " -> max score < 1, nothing to do"
	}

	// ② 起点文件。
	lv := n.levelFiles(best)
	ptr, hasPtr := n.ptr[best]
	start := lv[0]
	if hasPtr {
		found := false
		for _, file := range lv {
			if bytes.Compare(file.Min, ptr) > 0 {
				start = file
				found = true
				break
			}
		}
		if !found {
			start = lv[0]
			log += " (wrap)"
		}
	}
	log += fmt.Sprintf(" ptr=%q start=#%d[%q,%q]", ptr, start.ID, start.Min, start.Max)

	// ③ 下层重叠集 O。
	var o []File
	for _, file := range n.levelFiles(best + 1) {
		if naiveOverlap(file.Min, file.Max, start.Min, start.Max) {
			o = append(o, file)
		}
	}

	// ④ 扩张判定。
	rMin, rMax := start.Min, start.Max
	for _, file := range o {
		if bytes.Compare(file.Min, rMin) < 0 {
			rMin = file.Min
		}
		if bytes.Compare(file.Max, rMax) > 0 {
			rMax = file.Max
		}
	}
	var t []File
	for _, file := range lv {
		if naiveOverlap(file.Min, file.Max, rMin, rMax) {
			t = append(t, file)
		}
	}
	selected := []File{start}
	why := "T=={start}"
	if len(t) > 1 {
		var sum int64
		for _, file := range t {
			sum += file.Size
		}
		for _, file := range o {
			sum += file.Size
		}
		why = fmt.Sprintf("sum=%d !< X=%d", sum, n.X)
		if sum < n.X {
			tMin, tMax := t[0].Min, t[0].Max
			for _, file := range t[1:] {
				if bytes.Compare(file.Min, tMin) < 0 {
					tMin = file.Min
				}
				if bytes.Compare(file.Max, tMax) > 0 {
					tMax = file.Max
				}
			}
			var o2 []File
			for _, file := range n.levelFiles(best + 1) {
				if naiveOverlap(file.Min, file.Max, tMin, tMax) {
					o2 = append(o2, file)
				}
			}
			same := len(o) == len(o2)
			if same {
				set := map[uint64]bool{}
				for _, file := range o {
					set[file.ID] = true
				}
				for _, file := range o2 {
					if !set[file.ID] {
						same = false
					}
				}
			}
			why = "lower overlap grew"
			if same {
				selected = t
				why = "expanded"
			}
		}
	}
	log += fmt.Sprintf(" O=%v T=%v -> %s", naiveIDs(o), naiveIDs(t), why)

	// ⑤ 推进指针。
	advance := selected[0].Min
	for _, file := range selected[1:] {
		if bytes.Compare(file.Min, advance) > 0 {
			advance = file.Min
		}
	}
	n.ptr[best] = bytes.Clone(advance)
	log += fmt.Sprintf(" newptr=%q", advance)
	return selected, o, nil, log
}

func naiveIDs(files []File) []uint64 {
	out := make([]uint64, len(files))
	for i, file := range files {
		out[i] = file.ID
	}
	return out
}

func errKind(err error) string {
	if err == nil {
		return "nil"
	}
	for _, sentinel := range []error{
		ErrLevelOutOfRange, ErrBadRange, ErrNonPositiveSize,
		ErrDuplicateID, ErrOverlap, ErrNothingToCompact,
	} {
		if errors.Is(err, sentinel) {
			return sentinel.Error()
		}
	}
	return "unknown: " + err.Error()
}

// TestDifferential 用随机操作序列对拍 Picker 与朴素实现，
// 日志打印每步输入、输出与判定依据。
func TestDifferential(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 2026, 999983} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			p := mustNew(t, 3, []int64{20, 30}, 40)
			n := newNaive(3, []int64{20, 30}, 40)
			var nextID uint64 = 1

			key := func() []byte {
				return []byte{byte('a' + rng.Intn(10))}
			}
			checkState := func(step int) {
				t.Helper()
				for level := 1; level <= 3; level++ {
					got, err := p.Files(level)
					if err != nil {
						t.Fatalf("step %d: Files(%d): %v", step, level, err)
					}
					want := n.levelFiles(level)
					if !reflect.DeepEqual(ids(got), naiveIDs(want)) {
						t.Fatalf("step %d: level %d files %v != naive %v",
							step, level, ids(got), naiveIDs(want))
					}
				}
				for level := 1; level <= 2; level++ {
					gotKey, gotOK := p.Pointer(level)
					wantKey, wantOK := n.ptr[level]
					if gotOK != wantOK || string(gotKey) != string(wantKey) {
						t.Fatalf("step %d: ptr[%d] = %q,%v != naive %q,%v",
							step, level, gotKey, gotOK, wantKey, wantOK)
					}
				}
			}

			for step := 0; step < 400; step++ {
				if rng.Intn(100) < 60 {
					// AddFile：有意混入非法层号、倒置区间、非正字节数、
					// 重复编号与同层相交，验证拒绝路径一致。
					id := nextID
					nextID++
					if rng.Intn(100) < 8 && nextID > 2 {
						id = uint64(rng.Intn(int(nextID-1))) + 1
					}
					level := 1 + rng.Intn(3)
					if rng.Intn(100) < 5 {
						level = rng.Intn(6)
					}
					lo, hi := key(), key()
					if rng.Intn(100) < 6 {
						lo, hi = hi, lo
					}
					if bytes.Compare(lo, hi) > 0 {
						lo, hi = hi, lo
					}
					size := int64(1 + rng.Intn(15))
					if rng.Intn(100) < 5 {
						size = 0
					}
					file := File{ID: id, Level: level, Min: lo, Max: hi, Size: size}
					gotErr := p.AddFile(file)
					wantErr := n.addFile(file)
					t.Logf("step %d AddFile %+v -> got=%s want=%s",
						step, file, errKind(gotErr), errKind(wantErr))
					if errKind(gotErr) != errKind(wantErr) {
						t.Fatalf("step %d: AddFile error %v != naive %v", step, gotErr, wantErr)
					}
				} else {
					gotIn, gotO, gotErr := p.Pick()
					wantIn, wantO, wantErr, log := n.pick()
					t.Logf("step %d Pick -> got=(%v,%v,%s) want=(%v,%v,%s) | %s",
						step, ids(gotIn), ids(gotO), errKind(gotErr),
						naiveIDs(wantIn), naiveIDs(wantO), errKind(wantErr), log)
					if errKind(gotErr) != errKind(wantErr) {
						t.Fatalf("step %d: Pick error %v != naive %v", step, gotErr, wantErr)
					}
					if !reflect.DeepEqual(ids(gotIn), naiveIDs(wantIn)) ||
						!reflect.DeepEqual(ids(gotO), naiveIDs(wantO)) {
						t.Fatalf("step %d: Pick (%v,%v) != naive (%v,%v)",
							step, ids(gotIn), ids(gotO), naiveIDs(wantIn), naiveIDs(wantO))
					}
				}
				checkState(step)
			}
		})
	}
}

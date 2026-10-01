package fat12

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

// op 描述随机序列中的一步操作。
type op struct {
	kind string
	a, b int
}

// runRandomSequence 让实现与朴素模型执行完全相同的操作序列，逐步比对
// 映像、句柄、链、空闲数与 rover，并校验结构不变式。
func runRandomSequence(t *testing.T, seed int64, c, ops int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))

	f, err := New(c)
	if err != nil {
		t.Fatalf("seed=%d New(%d): %v", seed, c, err)
	}
	m := newModel(t, c)

	if !bytes.Equal(f.Image(), m.image()) {
		t.Fatalf("seed=%d initial image mismatch", seed)
	}

	// 现存句柄集合（两侧应一致）。
	handles := map[int]struct{}{}
	anyHandle := func() (int, bool) {
		for h := range handles {
			return h, true
		}
		return 0, false
	}

	t.Logf("seed=%d c=%d ops=%d ==== 随机序列开始", seed, c, ops)
	for i := 0; i < ops; i++ {
		var o op
		// 随机选择操作与参数，同时故意混入非法/不存在输入以验证拒绝顺序。
		switch rng.Intn(12) {
		case 0:
			o = op{"create", 1 + rng.Intn(c+3), 0}
		case 1:
			// 约 20% 概率非法 n
			if rng.Intn(5) == 0 {
				o = op{"create", rng.Intn(2) - 1, 0}
			} else {
				o = op{"create", 1 + rng.Intn(c+2), 0}
			}
		case 2:
			h, ok := anyHandle()
			if !ok {
				o = op{"extend", 2 + rng.Intn(c), 1 + rng.Intn(c+1)}
			} else {
				o = op{"extend", h, 1 + rng.Intn(c+1)}
			}
		case 3:
			if rng.Intn(4) == 0 {
				o = op{"extend", 2 + rng.Intn(c), rng.Intn(2) - 1}
			} else if h, ok := anyHandle(); ok {
				o = op{"extend", h, 1 + rng.Intn(c+2)}
			} else {
				o = op{"extend", 2 + rng.Intn(c), 1 + rng.Intn(c+1)}
			}
		case 4:
			if h, ok := anyHandle(); ok {
				o = op{"truncate", h, 1 + rng.Intn(c+1)}
			} else {
				o = op{"truncate", 2 + rng.Intn(c), 1 + rng.Intn(c+1)}
			}
		case 5:
			if h, ok := anyHandle(); ok {
				o = op{"truncate", h, rng.Intn(2) - 1}
			} else {
				o = op{"truncate", 2 + rng.Intn(c), rng.Intn(2) - 1}
			}
		case 6:
			if h, ok := anyHandle(); ok {
				o = op{"delete", h, 0}
			} else {
				o = op{"delete", 2 + rng.Intn(c), 0}
			}
		case 7:
			o = op{"markbad", 2 + rng.Intn(c+2), 0} // 少量越界
		case 8:
			if h, ok := anyHandle(); ok {
				o = op{"defrag", h, 0}
			} else {
				o = op{"defrag", 2 + rng.Intn(c), 0}
			}
		case 9:
			o = op{"markbad", 2 + rng.Intn(c), 0}
		case 10:
			if h, ok := anyHandle(); ok {
				o = op{"chain", h, 0}
			} else {
				o = op{"chain", 2 + rng.Intn(c), 0}
			}
		default:
			o = op{"query", 0, 0}
		}

		switch o.kind {
		case "create":
			got, ge := f.Create(o.a)
			want, mc := m.create(o.a)
			if errCat(ge) != mc {
				t.Fatalf("seed=%d step=%d Create(%d) err %v vs model %s", seed, i, o.a, ge, mc)
			}
			if ge == nil {
				if got != want {
					t.Fatalf("seed=%d step=%d Create(%d)=%d model=%d", seed, i, o.a, got, want)
				}
				handles[got] = struct{}{}
			} else {
				t.Logf("seed=%d step=%d Create(%d) 双方拒绝: %s（判定：错误类别一致）", seed, i, o.a, mc)
			}
		case "extend":
			ge := f.Extend(o.a, o.b)
			mc := m.extend(o.a, o.b)
			if errCat(ge) != mc {
				t.Fatalf("seed=%d step=%d Extend(%d,%d) err %v vs %s", seed, i, o.a, o.b, ge, mc)
			}
		case "truncate":
			ge := f.Truncate(o.a, o.b)
			mc := m.truncate(o.a, o.b)
			if errCat(ge) != mc {
				t.Fatalf("seed=%d step=%d Truncate(%d,%d) err %v vs %s", seed, i, o.a, o.b, ge, mc)
			}
		case "delete":
			ge := f.Delete(o.a)
			mc := m.del(o.a)
			if errCat(ge) != mc {
				t.Fatalf("seed=%d step=%d Delete(%d) err %v vs %s", seed, i, o.a, ge, mc)
			}
			if ge == nil {
				delete(handles, o.a)
			}
		case "markbad":
			ge := f.MarkBad(o.a)
			mc := m.markBad(o.a)
			if errCat(ge) != mc {
				t.Fatalf("seed=%d step=%d MarkBad(%d) err %v vs %s", seed, i, o.a, ge, mc)
			}
		case "defrag":
			got, ge := f.Defrag(o.a)
			want, mc := m.defrag(o.a)
			if errCat(ge) != mc || ge == nil && got != want {
				t.Fatalf("seed=%d step=%d Defrag(%d)=%d,%v model=%d,%s", seed, i, o.a, got, ge, want, mc)
			}
			if ge == nil {
				if got != o.a {
					delete(handles, o.a)
				}
				handles[got] = struct{}{}
			}
		case "chain":
			gc, ge := f.Chain(o.a)
			_, me := m.chainQuery(o.a)
			if errCat(ge) != me {
				t.Fatalf("seed=%d step=%d Chain(%d) err %v vs %s", seed, i, o.a, ge, me)
			}
			if ge == nil {
				wc := m.chain(o.a)
				if fmt.Sprint(gc) != fmt.Sprint(wc) {
					t.Fatalf("seed=%d step=%d Chain(%d)=%v model=%v", seed, i, o.a, gc, wc)
				}
			}
		case "query":
			m.log("query => free=%d rover=%d", f.Free(), f.Rover())
		}

		// 每步比对：映像（朴素编码）、Free、rover。
		if img := f.Image(); !bytes.Equal(img, m.image()) {
			t.Fatalf("seed=%d step=%d op=%+v image diverged\n got=%x\nwant=%x",
				seed, i, o, img, m.image())
		}
		if f.Free() != m.free() {
			t.Fatalf("seed=%d step=%d free %d vs %d", seed, i, f.Free(), m.free())
		}
		if f.Rover() != m.rover {
			t.Fatalf("seed=%d step=%d rover %d vs %d", seed, i, f.Rover(), m.rover)
		}
		// 文件表一致。
		for h := range handles {
			if !m.exists(h) {
				t.Fatalf("seed=%d step=%d stale handle %d", seed, i, h)
			}
		}
		assertInvariants(t, f, seed, i)
	}
	t.Logf("seed=%d ==== 序列结束：判定一致，free=%d rover=%d image=%x",
		seed, f.Free(), f.Rover(), f.Image())
}

// chainQuery 只做存在性判定（与 Chain 的错误语义一致）。
func (m *model) chainQuery(h int) ([]int, string) {
	if !m.exists(h) {
		m.log("Chain(%d) => ErrNotFound", h)
		return nil, "notfound"
	}
	ch := m.chain(h)
	m.log("Chain(%d) => %v", h, ch)
	return ch, ""
}

// assertInvariants 校验：链互不相交、无环、以链尾结束；Free 等于 0 项数；
// 坏簇为 0xFF7；保留项不变。
func assertInvariants(t *testing.T, f *FAT12, seed int64, step int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getEntry(0) != 0xFF8 || f.getEntry(1) != 0xFFF {
		t.Fatalf("seed=%d step=%d reserved entries changed", seed, step)
	}
	seen := map[int]bool{}
	free := 0
	for c := 2; c <= f.max; c++ {
		v := f.getEntry(c)
		switch {
		case v == 0:
			free++
		case v == 0xFF7:
		case v >= 0xFF8:
		default:
			if int(v) < 2 || int(v) > f.max {
				t.Fatalf("seed=%d step=%d cluster %d points out of range %03X", seed, step, c, v)
			}
		}
	}
	for h := range f.files {
		cur := h
		visited := map[int]bool{}
		for {
			if cur < 2 || cur > f.max {
				t.Fatalf("seed=%d step=%d chain out of range", seed, step)
			}
			if visited[cur] {
				t.Fatalf("seed=%d step=%d cycle at %d", seed, step, cur)
			}
			visited[cur] = true
			if seen[cur] {
				t.Fatalf("seed=%d step=%d cluster %d shared by two chains", seed, step, cur)
			}
			seen[cur] = true
			v := f.getEntry(cur)
			if v >= 0xFF8 {
				if v != 0xFFF {
					t.Fatalf("seed=%d step=%d eoc=%03X want FFF", seed, step, v)
				}
				break
			}
			cur = int(v)
		}
	}
	if free != f.freeCount() {
		t.Fatalf("seed=%d step=%d free count mismatch", seed, step)
	}
}

func TestRandomDifferential2000(t *testing.T) {
	const total = 2000
	// 边界 C 必测，其余随机。
	cs := make([]int, 0, total)
	cs = append(cs, 1, 2, 4078, 4077)
	rng := rand.New(rand.NewSource(20261001))
	for len(cs) < total {
		switch rng.Intn(3) {
		case 0:
			cs = append(cs, 1+rng.Intn(8)) // 小簇数，易触发边界与回卷
		case 1:
			cs = append(cs, 1+rng.Intn(64))
		default:
			cs = append(cs, 1+rng.Intn(4078))
		}
	}
	for i, c := range cs {
		seed := int64(i)*1000003 + 77
		ops := 40 + rng.Intn(120)
		runRandomSequence(t, seed, c, ops)
	}
}

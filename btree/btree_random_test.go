package btree

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// sim 是按题目规则逐步写成的朴素模拟：线性扫描、占用现算，
// 用于与 Tree 的每次操作结果对照。
type sim struct {
	C, P, M, Emax int
	pages         []simPage
	nextID        int
}

type simPage struct {
	id    int
	keys  []string
	sizes []int
}

func newSim(C, P int) *sim {
	return &sim{
		C: C, P: P, M: (C + 1) / 2, Emax: C / 4,
		pages:  []simPage{{id: 1}},
		nextID: 2,
	}
}

func (p *simPage) occ() int {
	s := 0
	for _, e := range p.sizes {
		s += e
	}
	return s
}

func (s *sim) locate(k string) int {
	idx := 0
	for i := 1; i < len(s.pages); i++ {
		if s.pages[i].keys[0] <= k {
			idx = i
		} else {
			break
		}
	}
	return idx
}

func (s *sim) sizeOf(k string) int {
	for _, p := range s.pages {
		for j, key := range p.keys {
			if key == k {
				return p.sizes[j]
			}
		}
	}
	return 0
}

func (s *sim) insert(k string, e int) (string, error) {
	if k == "" || e < 1 || e > s.Emax {
		return "判定: 参数非法", ErrInvalidArgument
	}
	i := s.locate(k)
	p := &s.pages[i]
	pos := 0
	for pos < len(p.keys) && p.keys[pos] < k {
		pos++
	}
	if pos < len(p.keys) && p.keys[pos] == k {
		return "判定: 键已存在", ErrKeyExists
	}
	if p.occ()+e > s.C && len(s.pages) == s.P {
		return "判定: 页数不足", ErrPageLimit
	}
	p.keys = append(p.keys, "")
	copy(p.keys[pos+1:], p.keys[pos:])
	p.keys[pos] = k
	p.sizes = append(p.sizes, 0)
	copy(p.sizes[pos+1:], p.sizes[pos:])
	p.sizes[pos] = e
	if p.occ() <= s.C {
		return fmt.Sprintf("判定: 放入页 %d 后占用 %d<=C", p.id, p.occ()), nil
	}
	n := len(p.keys)
	total := p.occ()
	best, bj, left := -1, 1, 0
	for j := 1; j < n; j++ {
		left += p.sizes[j-1]
		d := total - 2*left
		if d < 0 {
			d = -d
		}
		if best < 0 || d < best {
			best, bj = d, j
		}
	}
	np := simPage{id: s.nextID}
	s.nextID++
	np.keys = append([]string(nil), p.keys[bj:]...)
	np.sizes = append([]int(nil), p.sizes[bj:]...)
	p.keys = p.keys[:bj]
	p.sizes = p.sizes[:bj]
	s.pages = append(s.pages, simPage{})
	copy(s.pages[i+2:], s.pages[i+1:])
	s.pages[i+1] = np
	return fmt.Sprintf("判定: 切分页 %d，j=%d（差 %d），新页 %d", p.id, bj, best, np.id), nil
}

func (s *sim) delete(k string) (string, error) {
	if k == "" {
		return "判定: 参数非法", ErrInvalidArgument
	}
	i := s.locate(k)
	p := &s.pages[i]
	pos := -1
	for j, key := range p.keys {
		if key == k {
			pos = j
			break
		}
	}
	if pos < 0 {
		return "判定: 键不存在", ErrKeyNotFound
	}
	p.keys = append(p.keys[:pos], p.keys[pos+1:]...)
	p.sizes = append(p.sizes[:pos], p.sizes[pos+1:]...)
	if len(s.pages) == 1 {
		return "判定: 只剩一页，下溢不处理", nil
	}
	if p.occ() >= s.M {
		return fmt.Sprintf("判定: 页 %d 占用 %d>=M，不再平衡", p.id, p.occ()), nil
	}
	// (1) 左借
	if i > 0 {
		l := &s.pages[i-1]
		sum := 0
		for take := 1; take <= len(l.keys); take++ {
			sum += l.sizes[len(l.sizes)-take]
			if p.occ()+sum >= s.M {
				if l.occ()-sum >= s.M {
					mk := append([]string(nil), l.keys[len(l.keys)-take:]...)
					ms := append([]int(nil), l.sizes[len(l.sizes)-take:]...)
					p.keys = append(mk, p.keys...)
					p.sizes = append(ms, p.sizes...)
					l.keys = l.keys[:len(l.keys)-take]
					l.sizes = l.sizes[:len(l.sizes)-take]
					return fmt.Sprintf("判定: 左借 t=%d，从页 %d 到页 %d", take, l.id, p.id), nil
				}
				break
			}
		}
	}
	// (2) 右借
	if i < len(s.pages)-1 {
		r := &s.pages[i+1]
		sum := 0
		for take := 1; take <= len(r.keys); take++ {
			sum += r.sizes[take-1]
			if p.occ()+sum >= s.M {
				if r.occ()-sum >= s.M {
					p.keys = append(p.keys, r.keys[:take]...)
					p.sizes = append(p.sizes, r.sizes[:take]...)
					r.keys = append([]string(nil), r.keys[take:]...)
					r.sizes = append([]int(nil), r.sizes[take:]...)
					return fmt.Sprintf("判定: 右借 t=%d，从页 %d 到页 %d", take, r.id, p.id), nil
				}
				break
			}
		}
	}
	// (3) 并左
	if i > 0 {
		l := &s.pages[i-1]
		if l.occ()+p.occ() <= s.C {
			l.keys = append(l.keys, p.keys...)
			l.sizes = append(l.sizes, p.sizes...)
			s.pages = append(s.pages[:i], s.pages[i+1:]...)
			return fmt.Sprintf("判定: 并左，页 %d 并入页 %d", p.id, l.id), nil
		}
	}
	// (4) 并右
	if i < len(s.pages)-1 {
		r := &s.pages[i+1]
		if p.occ()+r.occ() <= s.C {
			p.keys = append(p.keys, r.keys...)
			p.sizes = append(p.sizes, r.sizes...)
			s.pages = append(s.pages[:i+1], s.pages[i+2:]...)
			return fmt.Sprintf("判定: 并右，页 %d 并入页 %d", r.id, p.id), nil
		}
	}
	return "判定: 四步都不满足，保持下溢", nil
}

func (s *sim) views() []PageView {
	out := make([]PageView, len(s.pages))
	for i, p := range s.pages {
		var keys []string
		if len(p.keys) > 0 {
			keys = append([]string(nil), p.keys...)
		}
		out[i] = PageView{ID: p.id, Keys: keys, Occupancy: p.occ()}
	}
	return out
}

// 与朴素模拟对照 2000 组随机操作序列：每步比较错误、页编号、
// 键列表与占用，并校验全部不变式；日志打印输入、输出与判定依据。
func TestRandomAgainstNaiveSimulation(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*1000003 + 7))
		C := 8 + rng.Intn(121)
		P := 1 + rng.Intn(12)
		tr, err := New(C, P)
		if err != nil {
			t.Fatalf("seq=%d New(%d,%d): %v", seq, C, P, err)
		}
		sm := newSim(C, P)
		nOps := 20 + rng.Intn(40)
		netSize := 0
		retired := map[int]bool{}
		prevIDs := map[int]bool{1: true}
		for op := 0; op < nOps; op++ {
			key := fmt.Sprintf("k%02d", rng.Intn(14))
			if rng.Intn(50) == 0 {
				key = "" // 非法：空键
			}
			mBefore := len(sm.pages)
			var decision string
			var treeErr, simErr error
			if rng.Intn(100) < 60 {
				size := 1 + rng.Intn(sm.Emax)
				if rng.Intn(40) == 0 {
					size = []int{0, sm.Emax + 1, sm.Emax + 2}[rng.Intn(3)] // 非法尺寸
				}
				decision, simErr = sm.insert(key, size)
				treeErr = tr.Insert(key, size)
				if simErr == nil {
					netSize += size
				}
				t.Logf("seq=%d op=%d 输入 Insert(%q,%d) C=%d P=%d | 输出 err=%v | %s",
					seq, op, key, size, C, P, treeErr, decision)
			} else {
				removed := sm.sizeOf(key)
				decision, simErr = sm.delete(key)
				treeErr = tr.Delete(key)
				if simErr == nil {
					netSize -= removed
				}
				t.Logf("seq=%d op=%d 输入 Delete(%q) C=%d P=%d | 输出 err=%v | %s",
					seq, op, key, C, P, treeErr, decision)
			}
			if (treeErr == nil) != (simErr == nil) || (treeErr != nil && !errors.Is(treeErr, simErr)) {
				t.Fatalf("seq=%d op=%d 错误不一致: tree=%v sim=%v", seq, op, treeErr, simErr)
			}
			got, want := tr.Pages(), sm.views()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seq=%d op=%d 页不一致:\n tree=%+v\n sim =%+v", seq, op, got, want)
			}
			// 定位页时的分隔键比较次数不超过 ceil(log2 m)（m 为定位时的页数）。
			// 参数非法的拒绝发生在定位之前，不产生比较。
			if !errors.Is(treeErr, ErrInvalidArgument) {
				bound := 0
				for (1 << bound) < mBefore {
					bound++
				}
				if tr.lastCmp > bound {
					t.Fatalf("seq=%d op=%d 分隔键比较次数 %d > ceil(log2 %d) = %d",
						seq, op, tr.lastCmp, mBefore, bound)
				}
			}
			checkInvariants(t, tr, got, netSize, retired, prevIDs)
			prevIDs = map[int]bool{}
			for _, p := range got {
				prevIDs[p.ID] = true
			}
		}
	}
}

// checkInvariants 校验：页序键严格递增、占用不超 C、无空页（唯一页除外）、
// 页编号不复用、尺寸总量守恒、分隔键比较次数不超过 ceil(log2 m)。
func checkInvariants(t *testing.T, tr *Tree, pages []PageView, netSize int, retired, prevIDs map[int]bool) {
	t.Helper()
	prevKey := ""
	total := 0
	for _, p := range pages {
		if p.Occupancy > tr.cap {
			t.Fatalf("页 %d 占用 %d 超过 C=%d", p.ID, p.Occupancy, tr.cap)
		}
		if len(p.Keys) == 0 && len(pages) > 1 {
			t.Fatalf("页 %d 为空但树中不止一页", p.ID)
		}
		if retired[p.ID] {
			t.Fatalf("页编号 %d 被复用", p.ID)
		}
		for _, k := range p.Keys {
			if prevKey != "" && k <= prevKey {
				t.Fatalf("键未严格递增: %q 后 %q", prevKey, k)
			}
			prevKey = k
		}
		total += p.Occupancy
	}
	if total != netSize {
		t.Fatalf("尺寸总量 %d != 成功插入-删除 %d", total, netSize)
	}
	for id := range prevIDs {
		still := false
		for _, p := range pages {
			if p.ID == id {
				still = true
				break
			}
		}
		if !still {
			retired[id] = true
		}
	}
}

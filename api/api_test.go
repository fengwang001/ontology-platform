package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/rel"
)

// refModel 是独立的朴素批量重算模型，用来对拍增量索引（不变量 1/2）。
type refModel struct {
	r map[int]int
	s map[int]map[int]bool
}

func newRef() *refModel { return &refModel{map[int]int{}, map[int]map[int]bool{}} }
func (m *refModel) joins(a int) []int {
	var o []int
	for c := range m.s[m.r[a]] {
		o = append(o, c)
	}
	sort.Ints(o)
	return o
}
func (m *refModel) size() (n int) {
	for _, b := range m.r {
		n += len(m.s[b])
	}
	return
}
func (m *refModel) delR(a, _ int) error {
	if _, ok := m.r[a]; !ok {
		return api.ErrNoSuchA
	}
	delete(m.r, a)
	return nil
}
func (m *refModel) addS(b, c int) error {
	if _, ok := m.s[b][c]; ok {
		return rel.ErrDuplicatePair
	}
	if m.s[b] == nil {
		m.s[b] = map[int]bool{}
	}
	m.s[b][c] = true
	return nil
}
func (m *refModel) delS(b, c int) error {
	if _, ok := m.s[b][c]; !ok {
		return rel.ErrMissingPair
	}
	delete(m.s[b], c)
	return nil
}

type call = func(int, int) error

func tab(s *api.System, m *refModel) (sy, mo [4]call) {
	sy = [4]call{
		func(a, b int) error { s.SetR(a, b); return nil },
		func(a, _ int) error { return s.DelR(a) }, s.AddS, s.DelS}
	mo = [4]call{
		func(a, b int) error { m.r[a] = b; return nil },
		m.delR, m.addS, m.delS}
	return
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestRandomSequences 多档随机序列逐步对拍；拒绝操作时模型不变、系统也必须不变
// （不留痕），且序列能继续（可继续使用），三类哨兵必须互异。
func TestRandomSequences(t *testing.T) {
	if api.ErrNoSuchA == rel.ErrMissingPair || rel.ErrMissingPair == rel.ErrDuplicatePair {
		t.Fatal("sentinel errors not distinct")
	}
	for _, seed := range []int64{1, 42, 99, 2026} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			s, m, r := api.New(), newRef(), rand.New(rand.NewSource(seed))
			sy, mo := tab(s, m)
			for k := 0; k < 2000; k++ {
				v, a, b, c := r.Intn(4), r.Intn(6), r.Intn(3)+1, r.Intn(5)
				x, y := b, c
				if v < 2 {
					x, y = a, b
				}
				if g, w := sy[v](x, y), mo[v](x, y); !errors.Is(g, w) || s.JoinSize() != m.size() {
					t.Fatalf("op %d: %v/%v", k, g, w)
				}
				for a := 0; a < 8; a++ {
					if !reflect.DeepEqual(s.Join(a), m.joins(a)) {
						t.Fatalf("op %d a=%d %v/%v", k, a, s.Join(a), m.joins(a))
					}
				}
			}
		})
	}
}

// TestConcurrentReaders 一个写者 4 步闭环重放，多读者各自固定次数单次只读；
// 每次返回必须命中某个写后串行快照（不撕裂）。全程无 sleep。
func TestConcurrentReaders(t *testing.T) {
	s, m := api.New(), newRef()
	sy, _ := tab(s, m)
	ops := [][3]int{{0, 0, 1}, {2, 1, 10}, {3, 1, 10}, {1, 0, 0}}
	okS, okJ := map[int]bool{}, map[string]bool{}
	rec := func() { okS[s.JoinSize()] = true; okJ[fmt.Sprint(s.Join(0))] = true }
	rec()
	for _, o := range ops {
		sy[o[0]](o[1], o[2])
		rec()
	}
	var bad, n int64
	var wg sync.WaitGroup
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				k := atomic.AddInt64(&n, 1)
				ok := okS[s.JoinSize()]
				if k&1 == 1 {
					ok = okJ[fmt.Sprint(s.Join(0))]
				}
				if !ok {
					atomic.StoreInt64(&bad, 1)
				}
			}
		}()
	}
	for i := 0; i < 500; i++ {
		for _, o := range ops {
			sy[o[0]](o[1], o[2])
		}
	}
	wg.Wait()
	if bad != 0 || n == 0 {
		t.Fatal("torn read observed or readers never ran")
	}
}

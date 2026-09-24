package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"ontology/api"
	"ontology/join"
	"ontology/jrow"
)

// 跑一段确定性随机操作序列，返回服务、模型表与完整变更日志。
func runSeq(seed int64, nops, keySpace int) (*api.Service, map[string]int, map[string]int, []jrow.Change) {
	s, err := api.New(keySpace * 2)
	if err != nil {
		panic(err)
	}
	rng := rand.New(rand.NewSource(seed))
	ml, mr := map[string]int{}, map[string]int{}
	var log []jrow.Change
	for i := 0; i < nops; i++ {
		k := fmt.Sprintf("k%03d", rng.Intn(keySpace))
		var chs []jrow.Change
		var err error
		switch rng.Intn(4) {
		case 0:
			v := rng.Intn(1000)
			chs, err = s.PutL(k, v)
			if err == nil {
				ml[k] = v
			}
		case 1:
			v := rng.Intn(1000)
			chs, err = s.PutR(k, v)
			if err == nil {
				mr[k] = v
			}
		case 2:
			chs, err = s.DelL(k)
			if err == nil {
				delete(ml, k)
			}
		default:
			chs, err = s.DelR(k)
			if err == nil {
				delete(mr, k)
			}
		}
		log = append(log, chs...)
	}
	return s, ml, mr, log
}

// 模型侧的值与本次操作写入值一致：Put 成功时模型直接记新值。
func chsValue(_ []jrow.Change, _ int) int { panic("占位") }

func naive(ml, mr map[string]int) []jrow.WideRow {
	var rows []jrow.WideRow
	for k, lv := range ml {
		if rv, ok := mr[k]; ok {
			rows = append(rows, jrow.WideRow{Key: k, LV: lv, RV: rv})
		}
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Key < rows[b].Key })
	return rows
}

// 不变量 1：任意操作序列后 WideTable 等于朴素 inner join。
func TestWideTableMatchesNaiveJoin(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		s, ml, mr, _ := runSeq(seed, 500, 40)
		got, want := s.WideTable(), naive(ml, mr)
		if len(got) != len(want) {
			t.Fatalf("seed=%d: 宽表 %d 行, 朴素 %d 行", seed, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("seed=%d: 第%d行 %+v != %+v", seed, i, got[i], want[i])
			}
		}
	}
}

// 不变量 2：变更日志每个前缀自洽，且最终下游等于朴素重算。
func TestChangelogPrefixesConsistent(t *testing.T) {
	for _, seed := range []int64{7, 8} {
		s, ml, mr, log := runSeq(seed, 500, 40)
		down := map[string][2]int{}
		for i, c := range log {
			if c.Op == '+' {
				down[c.Key] = [2]int{c.LV, c.RV}
			} else {
				cur, ok := down[c.Key]
				if !ok || cur != [2]int{c.LV, c.RV} {
					t.Fatalf("seed=%d 前缀%d: 撤回了不存在或不等的行 %+v", seed, i, c)
				}
				delete(down, c.Key)
			}
		}
		if len(down) != len(s.WideTable()) {
			t.Fatalf("seed=%d: 下游 %d 行 != 宽表", seed, len(down))
		}
		for _, w := range naive(ml, mr) {
			if down[w.Key] != [2]int{w.LV, w.RV} {
				t.Fatalf("seed=%d: 下游行 %+v 不符", seed, w)
			}
		}
	}
}

// 不变量 3：只匹配 key 才成行；重复 Put 幂等无输出。
func TestInnerJoinAndIdempotent(t *testing.T) {
	s, _ := api.New(10)
	cases := []struct {
		op      func() ([]jrow.Change, error)
		wantLen int
	}{
		{func() ([]jrow.Change, error) { return s.PutL("a", 1) }, 0}, // 单侧无输出
		{func() ([]jrow.Change, error) { return s.PutR("a", 2) }, 1}, // 成行
		{func() ([]jrow.Change, error) { return s.PutL("a", 1) }, 0}, // 重复幂等
		{func() ([]jrow.Change, error) { return s.PutR("a", 2) }, 0}, // 重复幂等
		{func() ([]jrow.Change, error) { return s.PutL("a", 9) }, 2}, // 更新先撤后加
		{func() ([]jrow.Change, error) { return s.DelR("a") }, 1},    // 撤行
	}
	for i, c := range cases {
		chs, err := c.op()
		if err != nil || len(chs) != c.wantLen {
			t.Fatalf("用例%d: 条数=%d err=%v 期望 %d", i, len(chs), err, c.wantLen)
		}
	}
}

// 不变量 4：四类错误互不相同且被拒后状态不变。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, err := api.New(0); !errors.Is(err, join.ErrBadMaxKeys) {
		t.Fatalf("maxKeys<=0 应报 ErrBadMaxKeys, 得 %v", err)
	}
	s, _ := api.New(2)
	if _, err := s.PutL("x", 1); err != nil {
		t.Fatal(err)
	}
	before := s.WideTable()
	rejects := []struct {
		op   func() error
		want error
	}{
		{func() error { _, e := s.PutL("", 1); return e }, join.ErrEmptyKey},
		{func() error { _, e := s.DelL("ghost"); return e }, join.ErrNotFound},
		{func() error { _, e := s.DelR("ghost"); return e }, join.ErrNotFound},
		{func() error { _, e := s.PutL("y", 1); return e }, nil}, // 占满容量
		{func() error { _, e := s.PutL("z", 1); return e }, join.ErrCapacity},
		{func() error { _, e := s.PutR("", 1); return e }, join.ErrEmptyKey},
	}
	for i, c := range rejects {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Fatalf("拒绝用例%d: err=%v 期望 %v", i, err, c.want)
		}
	}
	for _, e := range []error{join.ErrEmptyKey, join.ErrNotFound, join.ErrCapacity, join.ErrBadMaxKeys} {
		for _, e2 := range []error{join.ErrEmptyKey, join.ErrNotFound, join.ErrCapacity, join.ErrBadMaxKeys} {
			if (e == e2) != errors.Is(e, e2) {
				t.Fatalf("哨兵错误不互异: %v vs %v", e, e2)
			}
		}
	}
	after := s.WideTable()
	if len(before) != 0 || len(after) != 0 { // x、y 均未匹配,宽表应为空
		t.Fatalf("被拒操作改变了宽表: %v -> %v", before, after)
	}
	if _, err := s.PutR("x", 9); err != nil { // 拒绝后仍可正常使用
		t.Fatalf("拒绝后服务不可用: %v", err)
	}
}

// 并发：N 个 goroutine 各 PutL 一个已在 R 的 key，宽表恰 N 行，读数单调不减。
func TestConcurrentPutL(t *testing.T) {
	const N = 200
	s, _ := api.New(N * 2)
	for i := 0; i < N; i++ {
		if _, err := s.PutR(fmt.Sprintf("k%04d", i), i); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // 读方：行数始终在 [0,N] 且单调不减
		defer wg.Done()
		prev := 0
		for {
			n := len(s.WideTable())
			if n < prev || n > N {
				t.Errorf("行数 %d 越界或回退(前值 %d)", n, prev)
				return
			}
			prev = n
			select {
			case <-done:
				return
			default:
			}
		}
	}()
	var producers sync.WaitGroup
	for i := 0; i < N; i++ {
		producers.Add(1)
		go func(i int) {
			defer producers.Done()
			if _, err := s.PutL(fmt.Sprintf("k%04d", i), i*10); err != nil {
				t.Errorf("PutL: %v", err)
			}
		}(i)
	}
	producers.Wait()
	close(done)
	wg.Wait()
	if n := len(s.WideTable()); n != N {
		t.Fatalf("并发后宽表 %d 行, 期望 %d", n, N)
	}
}

// 自检方法必须直接通过。
func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

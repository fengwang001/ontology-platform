package fib

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"testing"
)

// random_test.go 用 1000+ 组随机操作序列把生产实现与独立朴素模型
// 逐步对照:每步比对错误类别与最少条目数,每个序列结束时对少量
// 地址范围内的全部地址穷举比对两个面及数据面列表,并打印每步的
// 输入、输出与判定依据(go test -v 可见)。

var nhPool = []string{"n1", "n2", "n3", "n4"}

func randNexthop(r *rand.Rand, seq int) Nexthop {
	switch x := r.IntN(20); {
	case x < 15:
		return NH(nhPool[r.IntN(len(nhPool))])
	case x < 18:
		return Blackhole
	default:
		return NH(fmt.Sprintf("u%d-%d", seq, r.IntN(1000)))
	}
}

func randPrefix(r *rand.Rand) Prefix {
	var base uint32
	switch r.IntN(6) {
	case 0:
		base = 10<<24 | (r.Uint32() & 0xFFFFFF)
	case 1:
		base = 11<<24 | (r.Uint32() & 0xFFFFFF)
	case 2:
		base = 172<<24 | 16<<16 | (r.Uint32() & 0xFFFF)
	case 3:
		base = 192<<24 | 168<<16 | (r.Uint32() & 0xFFFF)
	case 4:
		base = 10<<24 | 1<<16 | (r.Uint32() & 0xFFFF) // 密集窗口,制造重叠
	default:
		base = r.Uint32()
	}
	var l int
	switch r.IntN(12) {
	case 0:
		l = 0
	case 1, 2:
		l = 8
	case 3, 4, 5:
		l = 16
	case 6, 7, 8:
		l = 24
	case 9:
		l = 32
	default:
		l = r.IntN(33)
	}
	if l < 32 {
		base &= ^((uint32(1) << (32 - l)) - 1)
	}
	return Prefix{Addr: base, Len: l}
}

func randOp(r *rand.Rand, seq int, existing []Prefix) Op {
	// 有机会命中已有前缀,制造覆盖写与成功撤销。
	if len(existing) > 0 && r.IntN(100) < 35 {
		p := existing[r.IntN(len(existing))]
		if r.IntN(100) < 50 {
			return DeleteOp(p)
		}
		return PutOp(p, randNexthop(r, seq))
	}
	return PutOp(randPrefix(r), randNexthop(r, seq))
}

func existingPrefixes(nm *naiveModel) []Prefix {
	out := make([]Prefix, 0, len(nm.routes))
	for p := range nm.routes {
		out = append(out, p)
	}
	return out
}

func checkStep(t *testing.T, seq, step int, what string, gotErr, wantErr error, m *Manager, nm *naiveModel) {
	t.Helper()
	gotCount, wantCount := m.Count(), nm.optimalCount()
	ok := errIs(gotErr, wantErr) && gotCount == wantCount
	t.Logf("seq=%d step=%d 输入=%s 输出err=%v 判定: 错误类别一致=%v(期望%v) 条目数=%d(独立计算=%d) => %v",
		seq, step, what, gotErr, errIs(gotErr, wantErr), wantErr, gotCount, wantCount, ok)
	if !errIs(gotErr, wantErr) {
		t.Fatalf("seq=%d step=%d %s: 错误 %v, 朴素模型 %v", seq, step, what, gotErr, wantErr)
	}
	if gotCount != wantCount {
		t.Fatalf("seq=%d step=%d %s: 条目数 %d, 朴素模型独立计算 %d", seq, step, what, gotCount, wantCount)
	}
}

// exhaustiveWindow 对窗口内全部地址穷举比对:控制面、数据面、
// 数据面列表三者必须与朴素模型一致。
func exhaustiveWindow(t *testing.T, seq int, m *Manager, nm *naiveModel, base uint32, bits int) {
	t.Helper()
	entries := m.List()
	if len(entries) != m.Count() {
		t.Fatalf("seq=%d: List 长度 %d 与 Count %d 不一致", seq, len(entries), m.Count())
	}
	for i := 1; i < len(entries); i++ {
		a, b := entries[i-1].Prefix, entries[i].Prefix
		if a.Addr > b.Addr || (a.Addr == b.Addr && a.Len > b.Len) {
			t.Fatalf("seq=%d: List 未按(起始地址,前缀长度)升序", seq)
		}
	}
	n := uint32(1) << bits
	bad := 0
	for i := uint32(0); i < n; i++ {
		a := base + i
		ctl, data := m.Query(a)
		want := nm.controlQuery(a)
		listed := entriesLookup(entries, a)
		if ctl != want || data != want || listed != want {
			t.Logf("seq=%d 地址=%d.%d.%d.%d 控制面=%v 数据面=%v 列表=%v 朴素模型=%v",
				seq, byte(a>>24), byte(a>>16), byte(a>>8), byte(a), ctl, data, listed, want)
			bad++
			if bad > 5 {
				t.Fatalf("seq=%d: 窗口内存在大量不一致地址", seq)
			}
		}
	}
	if bad > 0 {
		t.Fatalf("seq=%d: 窗口内 %d 个地址不一致", seq, bad)
	}
	t.Logf("seq=%d 窗口=%d.%d.%d.%d/%d 全部 %d 个地址两平面及列表与朴素模型一致",
		seq, byte(base>>24), byte(base>>16), byte(base>>8), byte(base), 32-bits, n)
}

func TestRandomSequences(t *testing.T) {
	seed := uint64(20261007)
	if s := os.Getenv("FIB_SEED"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		seed = v
	}
	r := rand.New(rand.NewPCG(seed, 0x9e3779b9))
	sequences := 1200
	for seq := 0; seq < sequences; seq++ {
		cap := r.IntN(14)
		m := NewManager(cap)
		nm := newNaiveModel(cap)
		steps := 30
		for step := 0; step < steps; step++ {
			existing := existingPrefixes(nm)
			switch x := r.IntN(100); {
			case x < 45: // 写入
				p := randPrefix(r)
				nh := randNexthop(r, seq)
				err := m.Put(p, nh)
				checkStep(t, seq, step, fmt.Sprintf("Put(%s,%s)", prefixString(p), nexthopString(nh)),
					err, nm.put(p, nh), m, nm)
			case x < 65: // 撤销
				var p Prefix
				if len(existing) > 0 && r.IntN(100) < 70 {
					p = existing[r.IntN(len(existing))]
				} else {
					p = randPrefix(r)
				}
				err := m.Delete(p)
				checkStep(t, seq, step, fmt.Sprintf("Delete(%s)", prefixString(p)),
					err, nm.delete(p), m, nm)
			case x < 82: // 批量
				k := 2 + r.IntN(4)
				ops := make([]Op, 0, k)
				for i := 0; i < k; i++ {
					ops = append(ops, randOp(r, seq, existing))
				}
				// 偶尔注入非法参数与必然失败的撤销。
				switch r.IntN(10) {
				case 0:
					ops = append(ops, PutOp(Prefix{Addr: r.Uint32() | 1, Len: 8 + r.IntN(20)}, NH("x")))
				case 1:
					ops = append(ops, DeleteOp(Prefix{Addr: 203<<24 | r.Uint32()&0xFFFFFF, Len: 24}))
				}
				err := m.Batch(ops...)
				checkStep(t, seq, step, fmt.Sprintf("Batch(%d步)", len(ops)),
					err, nm.batch(ops...), m, nm)
			case x < 88: // 调整容量
				c := r.IntN(16)
				err := m.SetCapacity(c)
				checkStep(t, seq, step, fmt.Sprintf("SetCapacity(%d)", c),
					err, nm.setCapacity(c), m, nm)
			case x < 96: // 查询比对
				a := r.Uint32()
				ctl, data := m.Query(a)
				want := nm.controlQuery(a)
				ok := ctl == want && data == want
				t.Logf("seq=%d step=%d 输入=Query(%d.%d.%d.%d) 输出=(%v,%v) 判定: 与朴素模型一致=%v",
					seq, step, byte(a>>24), byte(a>>16), byte(a>>8), byte(a), ctl, data, ok)
				if !ok {
					t.Fatalf("seq=%d step=%d 地址 %d: 控制面=%v 数据面=%v 朴素模型=%v",
						seq, step, a, ctl, data, want)
				}
			default: // 列表一致性抽查
				entries := m.List()
				if len(entries) != m.Count() {
					t.Fatalf("seq=%d step=%d: List 长度 %d 与 Count %d 不一致",
						seq, step, len(entries), m.Count())
				}
				for i := 0; i < 20; i++ {
					a := r.Uint32()
					if got := entriesLookup(entries, a); got != nm.controlQuery(a) {
						t.Fatalf("seq=%d step=%d 地址 %d: 列表查询 %v, 朴素模型 %v",
							seq, step, a, got, nm.controlQuery(a))
					}
				}
			}
		}
		// 序列结束:对少量地址范围内的全部地址穷举比对。
		if seq%25 == 0 {
			exhaustiveWindow(t, seq, m, nm, r.Uint32()&0xFFFF0000, 16) // 整个 /16
		} else {
			exhaustiveWindow(t, seq, m, nm, r.Uint32()&0xFFFFF000, 12) // 整个 /20
		}
	}
}

// 并发调用等价于某个串行顺序:读写并发下,每次 Query 返回的两个面
// 必须来自同一次更新之后(控制面结果恒等于数据面结果)。
func TestConcurrent(t *testing.T) {
	m := NewManager(1 << 20)
	r := rand.New(rand.NewPCG(1, 2))
	var seedOps []Op
	for i := 0; i < 200; i++ {
		seedOps = append(seedOps, PutOp(randPrefix(r), randNexthop(r, 0)))
	}
	if err := m.Batch(seedOps...); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	errCh := make(chan string, 16)
	var writers, readers sync.WaitGroup
	// 写者:持续随机写入与撤销。
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			r := rand.New(rand.NewPCG(uint64(w), 7))
			for {
				select {
				case <-stop:
					return
				default:
				}
				if r.IntN(100) < 60 {
					_ = m.Put(randPrefix(r), randNexthop(r, w))
				} else {
					_ = m.Delete(randPrefix(r))
				}
			}
		}(w)
	}
	// 读者:校验每次查询的两个面一致。
	for w := 0; w < 4; w++ {
		readers.Add(1)
		go func(w int) {
			defer readers.Done()
			r := rand.New(rand.NewPCG(uint64(w), 9))
			for i := 0; i < 20000; i++ {
				a := r.Uint32()
				ctl, data := m.Query(a)
				if ctl != data {
					select {
					case errCh <- fmt.Sprintf("读者 %d: 地址 %d 控制面 %v != 数据面 %v", w, a, ctl, data):
					default:
					}
					return
				}
				if i%5000 == 0 {
					entries := m.List()
					for j := 1; j < len(entries); j++ {
						x, y := entries[j-1].Prefix, entries[j].Prefix
						if x.Addr > y.Addr || (x.Addr == y.Addr && x.Len > y.Len) {
							select {
							case errCh <- fmt.Sprintf("读者 %d: List 未排序", w):
							default:
							}
							return
						}
					}
				}
			}
		}(w)
	}
	// 读者有固定迭代量,读者全部完成后通知写者停止。
	readers.Wait()
	close(stop)
	writers.Wait()
	select {
	case msg := <-errCh:
		t.Fatal(msg)
	default:
	}
	// 收尾:并发停止后,列表长度与条目数一致,抽样两平面一致。
	if len(m.List()) != m.Count() {
		t.Fatal("并发结束后 List 与 Count 不一致")
	}
	r2 := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 1000; i++ {
		ctl, data := m.Query(r2.Uint32())
		if ctl != data {
			t.Fatalf("并发结束后地址两平面不一致: %v != %v", ctl, data)
		}
	}
}

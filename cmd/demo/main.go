package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"unsafe"

	"ontology/api"
	"ontology/handshake"
	"ontology/hs"
)

var failed bool

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	// 第三节八步重放（NOTES.md 分步表）
	h := handshake.New()
	i1, a1, e1 := h.RecvSYN("A", 1000)
	i2, a2, e2 := h.RecvSYN("A", 1000)
	i3, a3, e3 := h.RecvSYN("B", 2000)
	c4, s4, e4 := h.RecvACK("A", 1)
	_, _, e5 := h.RecvACK("A", 1)
	i6, a6, e6 := h.RecvSYN("C", 3000)
	i7, a7, e7 := h.RecvSYN("C", 9999)
	_, _, e8 := h.RecvACK("D", 999)
	ok("八步重放:返回与nextISN", e1 == nil && e3 == nil && e4 == nil && e6 == nil && e7 == nil &&
		i1 == 0 && a1 == 1001 && i3 == 1 && a3 == 2001 && c4 == 1000 && s4 == 0 &&
		i6 == 2 && a6 == 3001 && i7 == 3 && a7 == 10000 && h.NextISN() == 4)
	ok("第2步重复SYN原样重发", e2 == nil && i2 == i1 && a2 == a1)
	ok("第5步重复ACK幂等no-op", e5 == nil && h.Established("A"))
	ok("第8步孤儿ACK=ErrHalfOpen", errors.Is(e8, handshake.ErrHalfOpen))
	// 序号协商 +1 规则与三类哨兵错误
	x := handshake.New()
	si, sa, _ := x.RecvSYN("p", 77)
	ci, sj, ea := x.RecvACK("p", si+1)
	ok("ack==serverISN+1且SYN-ACK.ack==clientISN+1", sa == 78 && ea == nil && ci == 77 && sj == si)
	y := handshake.New()
	y.RecvSYN("q", 1)
	_, _, eBad := y.RecvACK("q", 99)
	_, _, eOrph := y.RecvACK("zz", 1)
	_, _, eSeq := y.RecvSYN("n", -1)
	ok("三类哨兵错误互异可判定", errors.Is(eBad, handshake.ErrBadAck) &&
		errors.Is(eOrph, handshake.ErrHalfOpen) && errors.Is(eSeq, handshake.ErrBadSeq) &&
		!errors.Is(eBad, handshake.ErrHalfOpen) && !errors.Is(eOrph, handshake.ErrBadSeq))
	_, _, eDone := y.RecvACK("q", 1)
	in, _, _ := y.RecvSYN("r", 9)
	ok("被拒后状态不变可继续", eDone == nil && y.Established("q") && in == 1)
	ok("半开表按src定位O(1)", probedIsConstant())
	ok("并发握手不串线", concurrentOK())
	ok("SelfCheck", api.New().SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}

// concurrentOK 用 N 个 goroutine 并发握手，校验不串线、serverISN 两两互异。
func concurrentOK() bool {
	s := api.New()
	const n = 32
	var wg sync.WaitGroup
	isns := make([]int64, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			src := fmt.Sprintf("c%d", i)
			isn, _, err := s.RecvSYN(src, int64(i))
			if err != nil {
				return
			}
			ci, si, err := s.RecvACK(src, isn+1)
			if err != nil || ci != int64(i) || si != isn {
				return
			}
			isns[i] = si
		}(i)
	}
	wg.Wait()
	seen := map[int64]bool{}
	for i, si := range isns {
		if seen[si] || !s.Established(fmt.Sprintf("c%d", i)) {
			return false
		}
		seen[si] = true
	}
	return true
}

// probed 读 hs.Server 的非导出计数器（反射读取字段，不经任何导出接口）。
func probed(s *hs.Server) int {
	f := reflect.ValueOf(s).Elem().FieldByName("probed")
	return int(reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Int())
}

// probedIsConstant 造 h 个半开连接后做一次查询，
// 检查个数不随 h 增长（证明是 O(1) 映射查找而非线性扫描）。
func probedIsConstant() bool {
	seen := map[int]bool{}
	for _, h := range []int{100, 1000, 10000} {
		s := hs.New()
		for i := 0; i < h; i++ {
			s.PutHalf(fmt.Sprintf("s%d", i), hs.Conn{ClientISN: int64(i), ServerISN: s.Alloc()})
		}
		s.LookupHalf("s7")
		seen[probed(s)] = true
	}
	return len(seen) == 1
}

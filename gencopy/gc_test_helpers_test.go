package gencopy

import (
	"encoding/binary"
	"sort"
	"testing"
)

func binaryU32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

// objSnapshot 是测试用的对象图快照：引用边 + 载荷。
type objSnapshot struct {
	id      Handle
	refs    []Handle
	payload []byte
}

// snapshotGraph 捕获当前从句柄 id 集合可见的全部存活对象图。
func snapshotGraph(t *testing.T, h *Heap, ids []Handle) map[Handle]*objSnapshot {
	t.Helper()
	out := make(map[Handle]*objSnapshot)
	var visit func(x Handle)
	visit = func(x Handle) {
		if x == Nil {
			return
		}
		if _, seen := out[x]; seen {
			return
		}
		n := numRefsForTest(t, h, x)
		sn := &objSnapshot{id: x, refs: make([]Handle, n)}
		for i := 0; i < n; i++ {
			r, err := h.GetRef(x, i)
			if err != nil {
				t.Fatalf("GetRef(%d,%d): %v", x, i, err)
			}
			sn.refs[i] = r
		}
		p, err := h.Payload(x)
		if err != nil {
			t.Fatalf("Payload(%d): %v", x, err)
		}
		sn.payload = p
		out[x] = sn
		for _, r := range sn.refs {
			visit(r)
		}
	}
	for _, id := range ids {
		visit(id)
	}
	return out
}

func numRefsForTest(t *testing.T, h *Heap, x Handle) int {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	l, err := h.resolve(x)
	if err != nil {
		t.Fatalf("resolve(%d): %v", x, err)
	}
	return h.numRefs(l)
}

// naiveReachable 是朴素全堆可达性 oracle：不区分代，直接从 roots 出发
// 在“回收前快照”上做对象引用的深度优先遍历，返回可达句柄集合。
func naiveReachable(pre map[Handle]*objSnapshot, roots []Handle) map[Handle]struct{} {
	reach := make(map[Handle]struct{})
	var visit func(x Handle)
	visit = func(x Handle) {
		if x == Nil {
			return
		}
		if _, seen := reach[x]; seen {
			return
		}
		sn, ok := pre[x]
		if !ok {
			return
		}
		reach[x] = struct{}{}
		for _, r := range sn.refs {
			visit(r)
		}
	}
	for _, r := range roots {
		visit(r)
	}
	return reach
}

func sortedHandles(m map[Handle]struct{}) []Handle {
	out := make([]Handle, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func allHandles(h *Heap) []Handle {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Handle, 0, len(h.objs))
	for k := range h.objs {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// assertUsedEqualsLive 校验各代已用字节等于其中存活对象字节之和。
func assertUsedEqualsLive(t *testing.T, h *Heap) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	var youngSum, oldSum int
	for _, l := range h.objs {
		addr := h.abs(l)
		size := objectSize(h.numRefsAt(addr),
			int(binaryU32(h.mem[addr+hdrPayloadLen:])))
		if l.region == regionOld {
			oldSum += size
		} else {
			youngSum += size
		}
	}
	youngUsed, oldUsed := h.eden.used+h.from.used, h.old.used
	if youngUsed != youngSum || oldUsed != oldSum {
		t.Fatalf("used bytes mismatch: young used=%d live=%d, old used=%d live=%d",
			youngUsed, youngSum, oldUsed, oldSum)
	}
}

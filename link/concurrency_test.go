package link

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

type testOp struct {
	src  ObjectInstanceID
	tgt  ObjectInstanceID
	disc map[string]string
	key  string
	del  bool
}

// naiveModel 是独立的朴素参考实现：全局一把锁、顺序执行，
// 只保留当前有效的链接集合与两个方向计数，不维护任何派生结构。
type naiveModel struct {
	links    map[string]bool
	fwd, bwd int
	fc, bc   int
}

func newNaive(fc, bc int) *naiveModel {
	return &naiveModel{links: map[string]bool{}, fc: fc, bc: bc}
}

func (m *naiveModel) opKey(src ObjectInstanceID, key string) string {
	if src == "a" {
		return "f|" + key
	}
	return "b|" + key
}

func (m *naiveModel) create(src ObjectInstanceID, key string) DecisionResult {
	k := m.opKey(src, key)
	if m.links[k] {
		return ResultDuplicate
	}
	if src == "a" {
		if m.fwd >= m.fc {
			return ResultRejected
		}
		m.fwd++
	} else {
		if m.bwd >= m.bc {
			return ResultRejected
		}
		m.bwd++
	}
	m.links[k] = true
	return ResultCreated
}

func (m *naiveModel) del(src ObjectInstanceID, key string) DecisionResult {
	k := m.opKey(src, key)
	if !m.links[k] {
		return ResultNotFound
	}
	delete(m.links, k)
	if src == "a" {
		m.fwd--
	} else {
		m.bwd--
	}
	return ResultDeleted
}

func generateOps(rng *rand.Rand, n int) []testOp {
	ops := make([]testOp, n)
	for i := range ops {
		src, tgt := ObjectInstanceID("a"), ObjectInstanceID("b")
		if rng.Intn(2) == 0 {
			src, tgt = "b", "a"
		}
		// 键空间小 => 大量重复与基数竞争；创建/删除随机交织。
		key := string(rune('0' + rng.Intn(4)))
		ops[i] = testOp{
			src: src, tgt: tgt, key: key,
			disc: disc("k", key),
			del:  rng.Intn(2) == 0,
		}
	}
	return ops
}

// TestConcurrentIsSerializable 将大量并发创建/删除随机交织后，按判定日志
// （记录于临界区内，Seq 即提交全序，且保持实时序）用独立朴素模型逐条重放：
// 每一条判定结果与终态计数都必须与朴素串行执行完全一致。
func TestConcurrentIsSerializable(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	objs := newMemObjects()
	objs.add("a", "A")
	objs.add("b", "B")

	for iter := 0; iter < 30; iter++ {
		rng := rand.New(rand.NewSource(int64(1000 + iter)))
		ops := generateOps(rng, 800)

		s := NewStore(objs)
		if err := s.RegisterType(LinkType{
			ID: "lt", SourceType: "A", TargetType: "B",
			ForwardCap: Limited(1), BackwardCap: Limited(2),
		}); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, op := range ops {
			wg.Add(1)
			go func(op testOp) {
				defer wg.Done()
				<-start
				if op.del {
					s.Delete("lt", op.src, op.tgt, op.disc)
					return
				}
				s.Create(CreateRequest{"lt", op.src, op.tgt, op.disc})
			}(op)
		}
		close(start)
		wg.Wait()

		logs := s.Decisions()
		if len(logs) != len(ops) {
			t.Fatalf("iter %d: %d ops but %d decisions", iter, len(ops), len(logs))
		}

		// Seq 必须从 1 连续递增。
		for i, d := range logs {
			if d.Seq != int64(i+1) {
				t.Fatalf("iter %d: non-contiguous seq at %d: %d", iter, i, d.Seq)
			}
		}

		// 按该全序用朴素模型重放，逐条核对结果类别。
		naive := newNaive(1, 2)
		for _, d := range logs {
			src := d.Request.SourceID
			key := d.Request.Discriminator["k"]
			var want DecisionResult
			if d.Op == "create" {
				want = naive.create(src, key)
			} else {
				want = naive.del(src, key)
			}
			if d.Result != want {
				t.Fatalf("iter %d seq %d: store=%s naive=%s op=%s %s/%s",
					iter, d.Seq, d.Result, want, d.Op, src, key)
			}
		}

		finalFwd := s.CountDirection("lt", "a", DirectionForward)
		finalBwd := s.CountDirection("lt", "b", DirectionBackward)
		if finalFwd > 1 || finalBwd > 2 {
			t.Fatalf("iter %d: cardinality violated fwd=%d bwd=%d", iter, finalFwd, finalBwd)
		}
		if finalFwd != naive.fwd || finalBwd != naive.bwd {
			t.Fatalf("iter %d: final counts store=(%d,%d) naive=(%d,%d)",
				iter, finalFwd, finalBwd, naive.fwd, naive.bwd)
		}

		// 终态登记集合也必须与朴素模型一致。
		if countActiveLinks(s) != len(naive.links) {
			t.Fatalf("iter %d: active link set size mismatch", iter)
		}
	}
}

// countActiveLinks 统计 Store 内部全部在库链接，仅测试使用。
func countActiveLinks(s *Store) int {
	total := 0
	for _, sh := range s.shards {
		sh.mu.Lock()
		for _, ps := range sh.pairs {
			total += len(ps.forward) + len(ps.backward)
		}
		sh.mu.Unlock()
	}
	return total
}

// TestConcurrentSameKeyCreateDelete 专盯“删除与同区分属性新建并发”：
// cap=1 下结果只能归约为删除在前或创建在前两种串行序之一。
func TestConcurrentSameKeyCreateDelete(t *testing.T) {
	objs := newMemObjects()
	objs.add("a", "A")
	objs.add("b", "B")

	for iter := 0; iter < 50; iter++ {
		s := NewStore(objs)
		if err := s.RegisterType(LinkType{
			ID: "lt", SourceType: "A", TargetType: "B",
			ForwardCap: Limited(1), BackwardCap: Limited(1),
		}); err != nil {
			t.Fatal(err)
		}

		// 初始占满唯一名额。
		mustCreate(t, s, CreateRequest{"lt", "a", "b", disc("k", "x")})

		var delErr, newErr error
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, delErr = s.Delete("lt", "a", "b", disc("k", "x")) }()
		go func() {
			defer wg.Done()
			<-start
			_, newErr = s.Create(CreateRequest{"lt", "a", "b", disc("k", "y")})
		}()
		close(start)
		wg.Wait()

		if delErr != nil {
			t.Fatalf("iter %d: delete failed: %v", iter, delErr)
		}
		// y 能否登记取决于全序：删除先 => 成功；创建先 => 基数满。
		if newErr != nil && !errors.Is(newErr, ErrCardinalityExceeded) {
			t.Fatalf("iter %d: unexpected create result: %v", iter, newErr)
		}

		cnt := s.CountDirection("lt", "a", DirectionForward)
		switch {
		case newErr == nil && cnt != 1:
			// 创建看到删除已释放名额：x 撤销、y 登记 => 1。
			t.Fatalf("iter %d: create-first serial order gives count %d", iter, cnt)
		case newErr != nil && cnt != 0:
			// 创建先于删除：y 因名额未释放被拒，随后 x 删除 => 0。
			t.Fatalf("iter %d: delete-first serial order gives count %d", iter, cnt)
		}
	}
}

package directory

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// logStep 在测试日志中打印输入、输出与判定依据。
func logStep(t *testing.T, op string, res Result, err error, basis string, args ...any) {
	t.Helper()
	in := fmt.Sprintf(op, args...)
	if err != nil {
		t.Logf("输入: %s | 输出: error=%q | 判定: %s", in, err.Error(), basis)
		return
	}
	t.Logf("输入: %s | 输出: value=%d hit=%t invalidations=%d empty=%d writebacks=%d | 判定: %s",
		in, res.Value, res.Hit, res.Invalidations, res.EmptyInvalidations, res.Writebacks, basis)
}

func expect(t *testing.T, got, want Result, basis string) {
	t.Helper()
	if got != want {
		t.Fatalf("结果不符 [%s]: got %+v want %+v", basis, got, want)
	}
	t.Logf("  ✓ %s", basis)
}

// checkInvariants 在任意操作后验证协议不变量。
func checkInvariants(t *testing.T, s *Simulator, ref map[int]int, basis string) {
	t.Helper()

	for b, d := range s.dir {
		mods := 0
		var soleMod int
		sharers := 0
		for i, ch := range s.caches {
			if line, ok := ch.peek(b); ok {
				if line.state == Modified {
					mods++
					soleMod = i
				} else {
					sharers++
				}
			}
		}
		// 任一块至多一个修改行；有修改行时没有共享行。
		if mods > 1 {
			t.Fatalf("[%s] 块 %d 有 %d 个修改行", basis, b, mods)
		}
		if mods == 1 && sharers > 0 {
			t.Fatalf("[%s] 块 %d 同时存在修改行(缓存%d)与共享行", basis, b, soleMod)
		}
		// 目录记录与实际修改者一致。
		if mods == 1 && d.modifier != soleMod {
			t.Fatalf("[%s] 块 %d 目录修改者=%d 实际修改者=%d", basis, b, d.modifier, soleMod)
		}
		if mods == 0 && d.modifier != -1 {
			t.Fatalf("[%s] 块 %d 目录记修改者 %d 但无实际修改行", basis, b, d.modifier)
		}
		if mods == 1 && (!d.holders[soleMod] || len(d.holders) != 1) {
			t.Fatalf("[%s] 块 %d 有修改者时集合应为仅 {%d}，实际 %v", basis, b, soleMod, d.holders)
		}
		// 实际持有者必在目录集合中（集合是超集，允许静默淘汰残留）。
		for i, ch := range s.caches {
			if ch.contains(b) && !d.holders[i] {
				t.Fatalf("[%s] 块 %d 缓存 %d 实际持有却不在目录集合 %v", basis, b, i, d.holders)
			}
		}
		// 无修改行时内存值等于最近写入值。
		if mods == 0 {
			if got := s.mem[b]; got != ref[b] {
				t.Fatalf("[%s] 块 %d 无修改行时内存=%d 期望最近写入值=%d", basis, b, got, ref[b])
			}
		}
	}
	for i, ch := range s.caches {
		if ch.len() > s.c {
			t.Fatalf("[%s] 缓存 %d 持有 %d 行，超过容量 %d", basis, i, ch.len(), s.c)
		}
	}
}

// TestModifierDowngradeOnRead 覆盖：修改者被另一个缓存读时写回内存、降为共享。
func TestModifierDowngradeOnRead(t *testing.T) {
	s, err := New(3, 4)
	if err != nil {
		t.Fatal(err)
	}
	ref := map[int]int{}

	res, err := s.Write(0, 10, 77)
	logStep(t, "Write(c=%d,b=%d,v=%d)", res, err, "冷缺失写：无集合成员，0 失效、0 写回，c0 持修改行", 0, 10, 77)
	expect(t, res, Result{Value: 77, Hit: false, Invalidations: 0, EmptyInvalidations: 0, Writebacks: 0}, "冷缺失写")
	ref[10] = 77
	checkInvariants(t, s, ref, "W(0,10,77) 后")

	res, err = s.Write(0, 10, 88)
	logStep(t, "Write(c=%d,b=%d,v=%d)", res, err, "修改命中：仅本地写，无消息", 0, 10, 88)
	expect(t, res, Result{Value: 88, Hit: true, Invalidations: 0, EmptyInvalidations: 0, Writebacks: 0}, "修改命中")
	ref[10] = 88

	// 读缺失：目录发现修改者 c0，c0 写回 88 并降为共享，c1 共享取得 88。
	res, err = s.Read(1, 10)
	logStep(t, "Read(c=%d,b=%d)", res, err, "读缺失：修改者 c0 写回 88(1 次写回)并降共享，c1 共享读得 88，0 失效", 1, 10)
	expect(t, res, Result{Value: 88, Hit: false, Invalidations: 0, EmptyInvalidations: 0, Writebacks: 1}, "修改者降级写回")
	if line, ok := s.caches[0].peek(10); !ok || line.state != Shared || line.value != 88 {
		t.Fatalf("c0 应持共享行值 88，实际 %+v", line)
	}
	if line, ok := s.caches[1].peek(10); !ok || line.state != Shared || line.value != 88 {
		t.Fatalf("c1 应持共享行值 88，实际 %+v", line)
	}
	if s.mem[10] != 88 {
		t.Fatalf("写回后内存应为 88，实际 %d", s.mem[10])
	}
	checkInvariants(t, s, ref, "R(1,10) 后")
}

// TestUpgradeWriteNoSelfInvalidation 覆盖：共享者升级写不向自己发失效，其余共享者被失效。
func TestUpgradeWriteNoSelfInvalidation(t *testing.T) {
	s, _ := New(4, 4)
	ref := map[int]int{}

	r, _ := s.Read(0, 5)
	logStep(t, "Read(c=%d,b=%d)", r, nil, "c0 冷读缺失，共享得 0", 0, 5)
	r, _ = s.Read(1, 5)
	logStep(t, "Read(c=%d,b=%d)", r, nil, "c1 冷读缺失，与 c0 同为共享者", 1, 5)
	r, _ = s.Read(2, 5)
	logStep(t, "Read(c=%d,b=%d)", r, nil, "c2 冷读缺失，三缓存共享", 2, 5)

	// c1 升级写：集合 {0,1,2}，向 0、2 发 2 条失效，自己不收；无修改者故 0 写回。
	r, _ = s.Write(1, 5, 33)
	logStep(t, "Write(c=%d,b=%d,v=%d)", r, nil, "c1 共享升级写：向 c0、c2 发 2 条失效（均命中，非空），不给自己发；无写回", 1, 5, 33)
	expect(t, r, Result{Value: 33, Hit: false, Invalidations: 2, EmptyInvalidations: 0, Writebacks: 0}, "升级写")
	ref[5] = 33
	if s.caches[0].contains(5) || s.caches[2].contains(5) {
		t.Fatal("c0、c2 的共享行应已被失效")
	}
	if line, ok := s.caches[1].peek(5); !ok || line.state != Modified || line.value != 33 {
		t.Fatalf("c1 应持修改行值 33，实际 %+v", line)
	}
	checkInvariants(t, s, ref, "升级写后")

	// 紧接写命中：0 失效 0 写回。
	r, _ = s.Write(1, 5, 34)
	logStep(t, "Write(c=%d,b=%d,v=%d)", r, nil, "修改命中：本地写，0 失效", 1, 5, 34)
	expect(t, r, Result{Value: 34, Hit: true, Invalidations: 0, EmptyInvalidations: 0, Writebacks: 0}, "修改命中")
	ref[5] = 34

	// 另一缓存写：修改者 c1 先写回 34，再对其发 1 条失效。
	r, _ = s.Write(3, 5, 35)
	logStep(t, "Write(c=%d,b=%d,v=%d)", r, nil, "c3 写缺失：c1 先写回 34(1 写回)再收 1 条失效；失效总数 1", 3, 5, 35)
	expect(t, r, Result{Value: 35, Hit: false, Invalidations: 1, EmptyInvalidations: 0, Writebacks: 1}, "写缺失遇修改者")
	ref[5] = 35
	if s.mem[5] != 34 {
		t.Fatalf("c1 应先写回旧值 34，内存实际 %d", s.mem[5])
	}
	checkInvariants(t, s, ref, "W(3,5,35) 后")
}

// TestSilentEvictionCausesEmptyInvalidation 覆盖：共享行静默淘汰后目录仍保留成员，
// 后续写发出的失效里有一条空失效。
func TestSilentEvictionCausesEmptyInvalidation(t *testing.T) {
	s, _ := New(2, 2)
	ref := map[int]int{}

	s.Read(0, 1)
	s.Read(1, 1)

	// c1 容量 2：读块 2 后再读块 3，块 1 为 LRU 最旧，被静默淘汰（共享、不通知目录）。
	s.Read(1, 2)
	r, _ := s.Read(1, 3)
	logStep(t, "Read(c=%d,b=%d)", r, nil, "c1 读块3：容量2满，静默淘汰共享行块1（不通知目录），无写回", 1, 3)
	expect(t, r, Result{Value: 0, Hit: false, Invalidations: 0, EmptyInvalidations: 0, Writebacks: 0}, "静默淘汰不产生消息")
	if s.caches[1].contains(1) {
		t.Fatal("块 1 应已被 c1 静默淘汰")
	}
	if !s.dir[1].holders[1] {
		t.Fatal("共享行静默淘汰不通知目录，集合中应仍含 c1")
	}

	// c0 升级写块 1：目录集合 {0,1}，向 c1 发失效，但 c1 已不持有 -> 空失效。
	r, _ = s.Write(0, 1, 9)
	logStep(t, "Write(c=%d,b=%d,v=%d)", r, nil, "c0 升级写块1：向集合成员 c1 发 1 条失效；c1 已静默淘汰 -> 空失效 1 条", 0, 1, 9)
	expect(t, r, Result{Value: 9, Hit: false, Invalidations: 1, EmptyInvalidations: 1, Writebacks: 0}, "空失效")
	ref[1] = 9
	checkInvariants(t, s, ref, "静默淘汰与空失效后")
}

// TestEvictModifiedWriteback 覆盖：缓存满时淘汰修改行，写回内存并从目录移除。
func TestEvictModifiedWriteback(t *testing.T) {
	s, _ := New(2, 2)
	ref := map[int]int{}

	s.Write(0, 1, 10)
	s.Write(0, 2, 20)
	// LRU：块1最旧、块2最新。写块3先淘汰块1（Modified）-> 写回 10、目录移除 c0。
	r, err := s.Write(0, 3, 30)
	logStep(t, "Write(c=%d,b=%d,v=%d)", r, err, "容量2满：淘汰最久未用的修改行块1，写回 10（1 写回）并从目录移除", 0, 3, 30)
	expect(t, r, Result{Value: 30, Hit: false, Invalidations: 0, EmptyInvalidations: 0, Writebacks: 1}, "淘汰修改行写回")
	ref[1] = 10
	ref[2] = 20
	ref[3] = 30
	if s.mem[1] != 10 {
		t.Fatalf("淘汰修改行应写回 10，内存实际 %d", s.mem[1])
	}
	if s.dir[1].modifier != -1 || s.dir[1].holders[0] {
		t.Fatalf("淘汰修改行后目录不应再记 c0：%+v", s.dir[1])
	}
	if s.caches[0].contains(1) {
		t.Fatal("块 1 应已被淘汰")
	}

	// c1 读块 1：目录已无修改者，直接从内存取到写回值 10。
	r, _ = s.Read(1, 1)
	logStep(t, "Read(c=%d,b=%d)", r, nil, "c1 读块1：目录无修改者，直接读内存写回值 10，0 写回", 1, 1)
	expect(t, r, Result{Value: 10, Hit: false, Invalidations: 0, EmptyInvalidations: 0, Writebacks: 0}, "读到写回值")
	checkInvariants(t, s, ref, "淘汰修改行写回后")
}

// TestLRURefreshOnHit 覆盖：读命中与写命中都刷新使用先后。
func TestLRURefreshOnHit(t *testing.T) {
	s, _ := New(1, 3)

	s.Read(0, 1)
	s.Read(0, 2)
	s.Read(0, 3)
	if got := s.caches[0].snapshot(); fmt.Sprint(got) != "[3 2 1]" {
		t.Fatalf("初始顺序(新->旧)应为 [3 2 1]，实际 %v", got)
	}

	r, _ := s.Read(0, 1)
	logStep(t, "Read(c=%d,b=%d)", r, nil, "读命中块1：返回本地值且刷新 LRU，新->旧顺序改为 1 3 2", 0, 1)
	if !r.Hit || r.Value != 0 {
		t.Fatalf("读命中应 hit/value=0，实际 %+v", r)
	}
	if got := s.caches[0].snapshot(); fmt.Sprint(got) != "[1 3 2]" {
		t.Fatalf("读命中后顺序(新->旧)应为 [1 3 2]，实际 %v", got)
	}

	r, _ = s.Write(0, 2, 2)
	logStep(t, "Write(c=%d,b=%d,v=%d)", r, nil, "共享行升级写块2：非命中，刷新 LRU", 0, 2, 2)
	if got := s.caches[0].snapshot(); fmt.Sprint(got) != "[2 1 3]" {
		t.Fatalf("升级写后顺序(新->旧)应为 [2 1 3]，实际 %v", got)
	}
	r, _ = s.Write(0, 2, 3)
	logStep(t, "Write(c=%d,b=%d,v=%d)", r, nil, "修改命中块2：本地写并刷新 LRU", 0, 2, 3)
	if !r.Hit {
		t.Fatalf("修改命中应 hit，实际 %+v", r)
	}

	r, _ = s.Read(0, 4)
	logStep(t, "Read(c=%d,b=%d)", r, nil, "读块4容量满：淘汰最旧块3（而非曾最旧的块1），证明命中刷新 LRU", 0, 4)
	if s.caches[0].contains(3) || !s.caches[0].contains(1) || !s.caches[0].contains(2) {
		t.Fatalf("命中刷新失效，淘汰结果：%v", s.caches[0].snapshot())
	}
}

// TestRejectionOrderAndNoStateChange 覆盖参数校验顺序，且被拒操作不得改变任何状态。
func TestRejectionOrderAndNoStateChange(t *testing.T) {
	if _, err := New(0, 4); err == nil {
		t.Fatal("N 不为正必须报错（第一个）")
	} else {
		t.Logf("New(N=0,C=4) -> %q | 判定: 先报 N 不为正", err)
	}
	if _, err := New(2, 0); err == nil {
		t.Fatal("C 不为正必须报错")
	} else {
		t.Logf("New(N=2,C=0) -> %q | 判定: N 合法后报 C 不为正", err)
	}

	s, _ := New(2, 2)
	s.Write(0, 1, 5)
	before := s.dump()

	if _, err := s.Read(2, -1); err == nil {
		t.Fatal("缓存编号越界必须报错")
	} else {
		t.Logf("Read(c=2,b=-1) -> %q | 判定: 先报缓存编号越界", err)
	}
	if _, err := s.Write(-1, -1, 1); err == nil {
		t.Fatal("缓存编号越界必须报错（写）")
	}
	if _, err := s.Read(0, -7); err == nil {
		t.Fatal("块号为负必须报错")
	} else {
		t.Logf("Read(c=0,b=-7) -> %q | 判定: 编号合法后报块号为负", err)
	}
	if after := s.dump(); after != before {
		t.Fatalf("被拒绝操作不得改变任何状态\n之前: %s\n之后: %s", before, after)
	}
	t.Log("拒绝后缓存/目录/内存/LRU 快照完全一致 | 判定: 拒绝操作无副作用")
}

// op 描述随机对拍中的一步操作。
type op struct {
	kind    byte // 'r' 或 'w'
	c, b, v int
}

// referenceModel 用单一内存真相独立重写协议规则，作为对拍参照（不依赖 Simulator 的目录实现）。
type referenceModel struct {
	n, c     int
	state    map[int]map[int]byte
	values   map[int]map[int]int
	lru      map[int][]int
	holders  map[int]map[int]bool
	modifier map[int]int
	mem      map[int]int
}

func newReference(n, c int) *referenceModel {
	m := &referenceModel{
		n:        n,
		c:        c,
		state:    map[int]map[int]byte{},
		values:   map[int]map[int]int{},
		lru:      map[int][]int{},
		holders:  map[int]map[int]bool{},
		modifier: map[int]int{},
		mem:      map[int]int{},
	}
	for i := 0; i < n; i++ {
		m.state[i] = map[int]byte{}
		m.values[i] = map[int]int{}
	}
	return m
}

func (m *referenceModel) touch(c, b int) {
	lru := m.lru[c]
	out := lru[:0]
	for _, x := range lru {
		if x != b {
			out = append(out, x)
		}
	}
	m.lru[c] = append(out, b)
}

func (m *referenceModel) ensure(b int) {
	if m.holders[b] == nil {
		m.holders[b] = map[int]bool{}
		m.modifier[b] = -1
	}
}

func (m *referenceModel) evictIfFull(c, b int) int {
	_, held := m.state[c][b]
	if held || len(m.lru[c]) < m.c {
		return 0
	}
	victim := m.lru[c][0]
	m.lru[c] = m.lru[c][1:]
	st := m.state[c][victim]
	victimVal := m.values[c][victim]
	delete(m.state[c], victim)
	delete(m.values[c], victim)
	if st == 'M' {
		m.ensure(victim)
		m.mem[victim] = victimVal
		delete(m.holders[victim], c)
		if m.modifier[victim] == c {
			m.modifier[victim] = -1
		}
		return 1
	}
	return 0
}

func (m *referenceModel) invalidate(h, b int) bool {
	if _, ok := m.state[h][b]; !ok {
		return false
	}
	delete(m.state[h], b)
	delete(m.values[h], b)
	lru := m.lru[h]
	out := lru[:0]
	for _, x := range lru {
		if x != b {
			out = append(out, x)
		}
	}
	m.lru[h] = out
	return true
}

func (m *referenceModel) do(o op) Result {
	c, b := o.c, o.b

	if o.kind == 'r' {
		if _, ok := m.state[c][b]; ok {
			m.touch(c, b)
			return Result{Value: m.values[c][b], Hit: true}
		}
		res := Result{Writebacks: m.evictIfFull(c, b)}
		m.ensure(b)
		if mod := m.modifier[b]; mod != -1 {
			m.mem[b] = m.values[mod][b]
			m.state[mod][b] = 'S'
			m.modifier[b] = -1
			m.holders[b][mod] = true
			res.Writebacks++
		}
		v := m.mem[b]
		m.state[c][b] = 'S'
		m.values[c][b] = v
		m.holders[b][c] = true
		m.touch(c, b)
		res.Value = v
		return res
	}

	v := o.v
	if m.state[c][b] == 'M' {
		m.values[c][b] = v
		m.touch(c, b)
		return Result{Value: v, Hit: true}
	}
	res := Result{Value: v, Writebacks: m.evictIfFull(c, b)}
	m.ensure(b)
	mod := m.modifier[b]
	if mod != -1 && mod != c {
		m.mem[b] = m.values[mod][b]
		res.Writebacks++
	}
	for h := range m.holders[b] {
		if h == c {
			continue
		}
		res.Invalidations++
		if !m.invalidate(h, b) {
			res.EmptyInvalidations++
		}
	}
	m.holders[b] = map[int]bool{c: true}
	m.modifier[b] = c
	m.state[c][b] = 'M'
	m.values[c][b] = v
	m.touch(c, b)
	return res
}

// TestRandomDifferential 用随机序列与单一内存参照对拍，
// 并校验“每个读返回该块最近一次写入的值”。
func TestRandomDifferential(t *testing.T) {
	const trials = 200
	for seed := int64(0); seed < trials; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(4)
		c := 1 + rng.Intn(3)
		s, _ := New(n, c)
		ref := newReference(n, c)

		const steps = 300
		lastWrite := map[int]int{}
		var lastOp op
		var got Result
		for i := 0; i < steps; i++ {
			o := op{c: rng.Intn(n), b: rng.Intn(6)}
			if rng.Intn(2) == 0 {
				o.kind = 'r'
				got, _ = s.Read(o.c, o.b)
			} else {
				o.kind = 'w'
				o.v = rng.Intn(1000)
				lastWrite[o.b] = o.v
				got, _ = s.Write(o.c, o.b, o.v)
			}
			want := ref.do(o)
			if got != want {
				t.Fatalf("对拍不符 seed=%d step=%d op=%+v\ngot =%+v\nwant=%+v", seed, i, o, got, want)
			}
			if o.kind == 'r' && got.Value != lastWrite[o.b] {
				t.Fatalf("seed=%d step=%d 读块%d 得 %d，最近写入值应为 %d", seed, i, o.b, got.Value, lastWrite[o.b])
			}
			lastOp = o
		}
		format := "Read(c=%d,b=%d)"
		args := []any{lastOp.c, lastOp.b}
		if lastOp.kind == 'w' {
			format = "Write(c=%d,b=%d,v=%d)"
			args = append(args, lastOp.v)
		}
		logStep(t, format, got, nil,
			fmt.Sprintf("seed=%d 300 步全部与参照模型一致，读值=最近写入值", seed),
			args...)
	}
	t.Logf("随机对拍 %d 个种子全部通过", trials)
}

// replay 在新模拟器上重放操作序列并返回结果。
func replay(t *testing.T, n, c int, ops []op) []Result {
	t.Helper()
	s, err := New(n, c)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]Result, len(ops))
	for i, o := range ops {
		if o.kind == 'r' {
			out[i], err = s.Read(o.c, o.b)
		} else {
			out[i], err = s.Write(o.c, o.b, o.v)
		}
		if err != nil {
			t.Fatalf("step %d 非法操作 %+v: %v", i, o, err)
		}
	}
	return out
}

// TestDeterministicReplay 覆盖：相同操作序列重放结果完全相同。
func TestDeterministicReplay(t *testing.T) {
	const n, c = 3, 2
	rng := rand.New(rand.NewSource(4242))
	ops := make([]op, 400)
	for i := range ops {
		o := op{c: rng.Intn(n), b: rng.Intn(8)}
		if rng.Intn(2) == 0 {
			o.kind = 'r'
		} else {
			o.kind = 'w'
			o.v = rng.Intn(1000)
		}
		ops[i] = o
	}
	first := replay(t, n, c, ops)
	for k := 0; k < 3; k++ {
		again := replay(t, n, c, ops)
		for i := range first {
			if first[i] != again[i] {
				t.Fatalf("第 %d 次重放 step %d 结果不同: %+v != %+v", k+1, i, again[i], first[i])
			}
		}
	}
	t.Logf("400 步操作序列重放 4 次，结果（值/命中/失效/空失效/写回）逐条完全一致")
}

// TestConcurrentLinearizability 并发调用不产生数据竞争，
// 且终态满足协议全部不变量（与某个串行先后等价）。
func TestConcurrentLinearizability(t *testing.T) {
	s, _ := New(4, 3)
	const workers = 8
	const rounds = 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < rounds; i++ {
				c := rng.Intn(4)
				b := rng.Intn(5)
				if rng.Intn(2) == 0 {
					_, _ = s.Read(c, b)
				} else {
					_, _ = s.Write(c, b, int(seed)*rounds+i)
				}
			}
		}(int64(w) + 1)
	}
	wg.Wait()

	ref := map[int]int{}
	for b, d := range s.dir {
		if d.modifier != -1 {
			ref[b] = s.caches[d.modifier].items[b].Value.(*cacheLine).value
		} else {
			ref[b] = s.mem[b]
		}
	}
	checkInvariants(t, s, ref, "8x200 并发操作后")
	t.Logf("8 个工作协程各 200 次并发读写无数据竞争，终态通过全部不变量检查（与某一串行先后等价）")
}

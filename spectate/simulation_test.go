package spectate_test

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/live"
	"ontology/spectate"
)

// ---------- 朴素模拟：逐事件线性扫描，作为对照实现 ----------

type naiveViewer struct {
	judge  bool
	cursor int64
}

type naive struct {
	d, m    int64
	now     int64
	mode    spectate.Mode
	events  []live.Event
	ended   bool
	tEnd    int64
	friends map[string]bool
	viewers map[string]*naiveViewer
	nonJudg int64
}

func newNaive(d, m int64) *naive {
	return &naive{
		d:       d,
		m:       m,
		mode:    spectate.Public,
		friends: map[string]bool{},
		viewers: map[string]*naiveViewer{},
	}
}

func (n *naive) cutoffAt(now int64, judge bool) int64 {
	if judge {
		return now
	}
	if !n.ended {
		return now - n.d
	}
	c := 2*now - n.tEnd - n.d
	if c > n.tEnd {
		c = n.tEnd
	}
	return c
}

func (n *naive) hiddenOKAt(cutoff int64, judge bool) bool {
	return judge || (n.ended && cutoff == n.tEnd)
}

func validNowOp(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func (n *naive) checkClock(now int64) error {
	if now < n.now {
		return spectate.ErrClockRegression
	}
	return nil
}

func (n *naive) emit(now int64, k live.Kind) (int64, error) {
	if !validNowOp(now) || !k.Valid() {
		return 0, spectate.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return 0, err
	}
	if n.ended {
		return 0, live.ErrEnded
	}
	ev := live.Event{Seq: int64(len(n.events)) + 1, T: now, Kind: k}
	n.events = append(n.events, ev)
	if k == live.End {
		n.ended = true
		n.tEnd = now
	}
	n.now = now
	return ev.Seq, nil
}

func (n *naive) befriend(now int64, name string) error {
	if !validNowOp(now) || name == "" {
		return spectate.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	n.friends[name] = true
	n.now = now
	return nil
}

func (n *naive) unfriend(now int64, name string) error {
	if !validNowOp(now) || name == "" {
		return spectate.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	delete(n.friends, name)
	if n.mode == spectate.FriendsOnly {
		if v, ok := n.viewers[name]; ok && !v.judge {
			delete(n.viewers, name)
			n.nonJudg--
		}
	}
	n.now = now
	return nil
}

func (n *naive) join(now int64, name string, judge bool) error {
	if !validNowOp(now) || name == "" {
		return spectate.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if _, ok := n.viewers[name]; ok {
		return spectate.ErrAlreadyWatching
	}
	if !judge {
		switch {
		case n.mode == spectate.Off:
			return spectate.ErrModeOff
		case n.mode == spectate.FriendsOnly && !n.friends[name]:
			return spectate.ErrNotFriend
		case n.nonJudg >= n.m:
			return spectate.ErrFull
		}
	}
	n.viewers[name] = &naiveViewer{judge: judge}
	if !judge {
		n.nonJudg++
	}
	n.now = now
	return nil
}

func (n *naive) leave(now int64, name string) error {
	if !validNowOp(now) || name == "" {
		return spectate.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	v, ok := n.viewers[name]
	if !ok {
		return spectate.ErrNotWatching
	}
	delete(n.viewers, name)
	if !v.judge {
		n.nonJudg--
	}
	n.now = now
	return nil
}

func (n *naive) setMode(now int64, mode spectate.Mode) error {
	if !validNowOp(now) || !mode.Valid() {
		return spectate.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	n.mode = mode
	for name, v := range n.viewers {
		if v.judge {
			continue
		}
		if mode == spectate.Off || (mode == spectate.FriendsOnly && !n.friends[name]) {
			delete(n.viewers, name)
			n.nonJudg--
		}
	}
	n.now = now
	return nil
}

// pull 逐事件线性扫描：遇到 t > cutoff 即停，不可投递的 Hidden 跳过并越过。
func (n *naive) pull(now int64, name string, maxN int64) ([]int64, bool, error) {
	if !validNowOp(now) || name == "" || maxN < 1 || maxN > 1000 {
		return nil, false, spectate.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return nil, false, err
	}
	v, ok := n.viewers[name]
	if !ok {
		return nil, false, spectate.ErrNotWatching
	}
	cutoff := n.cutoffAt(now, v.judge)
	hok := n.hiddenOKAt(cutoff, v.judge)
	out := []int64{}
	for seq := v.cursor + 1; seq <= int64(len(n.events)); seq++ {
		ev := n.events[seq-1]
		if ev.T > cutoff {
			break
		}
		if ev.Kind == live.Hidden && !hok {
			v.cursor = seq
			continue
		}
		out = append(out, seq)
		v.cursor = seq
		if int64(len(out)) == maxN {
			break
		}
	}
	more := false
	for seq := v.cursor + 1; seq <= int64(len(n.events)); seq++ {
		ev := n.events[seq-1]
		if ev.T > cutoff {
			break
		}
		if ev.Kind == live.Hidden && !hok {
			continue
		}
		more = true
		break
	}
	n.now = now
	return out, more, nil
}

func (n *naive) lag(now int64, name string) (int64, error) {
	if !validNowOp(now) || name == "" {
		return 0, spectate.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return 0, err
	}
	v, ok := n.viewers[name]
	if !ok {
		return 0, spectate.ErrNotWatching
	}
	cutoff := n.cutoffAt(now, v.judge)
	hok := n.hiddenOKAt(cutoff, v.judge)
	count := int64(0)
	for seq := v.cursor + 1; seq <= int64(len(n.events)); seq++ {
		ev := n.events[seq-1]
		if ev.T > cutoff {
			break
		}
		if ev.Kind == live.Hidden && !hok {
			continue
		}
		count++
	}
	n.now = now
	return count, nil
}

// ---------- 随机操作序列生成与对照驱动 ----------

type opKind int

const (
	opEmit opKind = iota
	opBefriend
	opUnfriend
	opJoin
	opLeave
	opSetMode
	opPull
	opLag
)

type op struct {
	kind   opKind
	now    int64
	name   string
	judge  bool
	maxN   int64
	mode   spectate.Mode
	evKind live.Kind
}

func (o op) String() string {
	switch o.kind {
	case opEmit:
		return fmt.Sprintf("Emit(now=%d, kind=%d)", o.now, o.evKind)
	case opBefriend:
		return fmt.Sprintf("Befriend(now=%d, %q)", o.now, o.name)
	case opUnfriend:
		return fmt.Sprintf("Unfriend(now=%d, %q)", o.now, o.name)
	case opJoin:
		return fmt.Sprintf("Join(now=%d, %q, judge=%v)", o.now, o.name, o.judge)
	case opLeave:
		return fmt.Sprintf("Leave(now=%d, %q)", o.now, o.name)
	case opSetMode:
		return fmt.Sprintf("SetMode(now=%d, mode=%d)", o.now, o.mode)
	case opPull:
		return fmt.Sprintf("Pull(now=%d, %q, maxN=%d)", o.now, o.name, o.maxN)
	case opLag:
		return fmt.Sprintf("Lag(now=%d, %q)", o.now, o.name)
	}
	return "?"
}

func genOps(r *rand.Rand, d int64) []op {
	names := []string{"a", "b", "c", "d", "j"}
	n := 40 + r.Intn(60)
	ops := make([]op, 0, n)
	now := int64(0)
	for i := 0; i < n; i++ {
		now += r.Int63n(4)
		if d > 0 && r.Intn(100) < 15 { // 偶发大跳，覆盖赛后追平
			now += r.Int63n(d + 1)
		}
		name := func() string {
			if r.Intn(100) < 3 {
				return "" // 偶发非法参数
			}
			return names[r.Intn(len(names))]
		}
		switch x := r.Intn(100); {
		case x < 28:
			k := live.Normal
			switch y := r.Intn(100); {
			case y < 25:
				k = live.Hidden
			case y < 33:
				k = live.End
			case y < 35:
				k = live.Kind(99)
			}
			ops = append(ops, op{kind: opEmit, now: now, evKind: k})
		case x < 51:
			ops = append(ops, op{kind: opJoin, now: now, name: name(), judge: r.Intn(4) == 0})
		case x < 58:
			ops = append(ops, op{kind: opLeave, now: now, name: name()})
		case x < 66:
			ops = append(ops, op{kind: opBefriend, now: now, name: name()})
		case x < 74:
			ops = append(ops, op{kind: opUnfriend, now: now, name: name()})
		case x < 79:
			m := spectate.Mode(r.Intn(3))
			if r.Intn(100) < 3 {
				m = spectate.Mode(99)
			}
			ops = append(ops, op{kind: opSetMode, now: now, mode: m})
		case x < 90:
			maxN := 1 + r.Int63n(5)
			if r.Intn(100) < 3 {
				maxN = []int64{0, 1001}[r.Intn(2)]
			}
			ops = append(ops, op{kind: opPull, now: now, name: name(), maxN: maxN})
		default:
			ops = append(ops, op{kind: opLag, now: now, name: name()})
		}
	}
	return ops
}

// res 统一编码各操作结果，用于三路（实现 / 朴素 / 重放）对比。
type res struct {
	n    int64 // Emit 的 seq 或 Lag 的计数
	seqs []int64
	more bool
	err  error
}

// sameRes 比较两个结果；nil 与空切片视为相同（错误路径不保证返回空切片）。
func sameRes(a, b res) bool {
	return a.n == b.n && a.more == b.more && a.err == b.err && slices.Equal(a.seqs, b.seqs)
}

func runReal(svc *spectate.Service, o op) res {
	switch o.kind {
	case opEmit:
		ev, err := svc.Emit(o.now, o.evKind)
		return res{n: ev.Seq, err: err}
	case opBefriend:
		return res{err: svc.Befriend(o.now, o.name)}
	case opUnfriend:
		return res{err: svc.Unfriend(o.now, o.name)}
	case opJoin:
		return res{err: svc.Join(o.now, o.name, o.judge)}
	case opLeave:
		return res{err: svc.Leave(o.now, o.name)}
	case opSetMode:
		return res{err: svc.SetMode(o.now, o.mode)}
	case opPull:
		out, more, err := svc.Pull(o.now, o.name, o.maxN)
		return res{seqs: seqsOf(out), more: more, err: err}
	case opLag:
		n, err := svc.Lag(o.now, o.name)
		return res{n: n, err: err}
	}
	panic("bad op")
}

func (n *naive) step(o op) res {
	switch o.kind {
	case opEmit:
		seq, err := n.emit(o.now, o.evKind)
		return res{n: seq, err: err}
	case opBefriend:
		return res{err: n.befriend(o.now, o.name)}
	case opUnfriend:
		return res{err: n.unfriend(o.now, o.name)}
	case opJoin:
		return res{err: n.join(o.now, o.name, o.judge)}
	case opLeave:
		return res{err: n.leave(o.now, o.name)}
	case opSetMode:
		return res{err: n.setMode(o.now, o.mode)}
	case opPull:
		seqs, more, err := n.pull(o.now, o.name, o.maxN)
		return res{seqs: seqs, more: more, err: err}
	case opLag:
		c, err := n.lag(o.now, o.name)
		return res{n: c, err: err}
	}
	panic("bad op")
}

// TestRandomAgainstNaive 1500 组随机操作序列：实现 vs 朴素逐事件扫描 vs 重放，
// 三路结果逐项一致；同时校验每条投递的不变量（判定依据）。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	ds := []int64{0, 1, 2, 3, 7, 100, 3001}
	for seed := int64(0); seed < sequences; seed++ {
		r := rand.New(rand.NewSource(seed))
		d := ds[r.Intn(len(ds))]
		m := int64(1 + r.Intn(5))
		ops := genOps(r, d)

		svc, err := spectate.New(d, m)
		if err != nil {
			t.Fatal(err)
		}
		replay, _ := spectate.New(d, m)
		nb := newNaive(d, m)

		// 不变量跟踪：事件表、结束状态、各观战者身份与已收最大 seq。
		var events []live.Event
		ended := false
		tEnd := int64(0)
		mode := spectate.Public
		friends := map[string]bool{}
		judgeOf := map[string]bool{} // 仅含在观战者
		lastSeq := map[string]int64{}
		removeViewer := func(name string) {
			delete(judgeOf, name)
			delete(lastSeq, name)
		}

		fail := func(i int, o op, got, want res, basis string) {
			hist := ""
			for k := 0; k <= i; k++ {
				hist += fmt.Sprintf("    op[%d]: %s\n", k, ops[k])
			}
			t.Fatalf("seed=%d D=%d M=%d\n%s  实现: %+v\n  对照: %+v\n  判定依据: %s",
				seed, d, m, hist, got, want, basis)
		}

		for i, o := range ops {
			got := runReal(svc, o)
			want := nb.step(o)
			again := runReal(replay, o)
			if !sameRes(got, want) {
				fail(i, o, got, want, "与逐事件扫描朴素模拟结果不一致")
			}
			if !sameRes(got, again) {
				fail(i, o, got, again, "相同操作序列重放结果不一致")
			}
			// 跟踪状态并校验不变量。
			switch o.kind {
			case opEmit:
				if got.err == nil {
					events = append(events, live.Event{Seq: got.n, T: o.now, Kind: o.evKind})
					if o.evKind == live.End {
						ended, tEnd = true, o.now
					}
				}
			case opJoin:
				if got.err == nil {
					judgeOf[o.name] = o.judge
					lastSeq[o.name] = 0
				}
			case opLeave:
				if got.err == nil {
					removeViewer(o.name)
				}
			case opBefriend:
				if got.err == nil {
					friends[o.name] = true
				}
			case opUnfriend:
				if got.err == nil {
					delete(friends, o.name)
					if mode == spectate.FriendsOnly {
						if j, ok := judgeOf[o.name]; ok && !j {
							removeViewer(o.name)
						}
					}
				}
			case opSetMode:
				if got.err == nil {
					mode = o.mode
					for name, j := range judgeOf {
						if !j && (mode == spectate.Off || (mode == spectate.FriendsOnly && !friends[name])) {
							removeViewer(name)
						}
					}
				}
			case opPull:
				if got.err != nil {
					continue
				}
				judge := judgeOf[o.name]
				cutoff := o.now
				if !judge {
					if !ended {
						cutoff = o.now - d
					} else {
						cutoff = 2*o.now - tEnd - d
						if cutoff > tEnd {
							cutoff = tEnd
						}
					}
				}
				prev := lastSeq[o.name]
				for _, s := range got.seqs {
					ev := events[s-1]
					if s <= prev {
						fail(i, o, got, want, "观战者收到的 seq 未严格递增")
					}
					if ev.T > cutoff {
						fail(i, o, got, want, "非裁判收到 t > 当时 cutoff 的事件")
					}
					if ev.Kind == live.Hidden && !judge && !(ended && cutoff == tEnd) {
						fail(i, o, got, want, "对局追平前非裁判收到 Hidden")
					}
					prev = s
				}
				lastSeq[o.name] = prev
			}
		}
		t.Logf("seed=%d D=%d M=%d ops=%d 判定通过：实现==朴素模拟==重放，且投递不变量成立",
			seed, d, m, len(ops))
		if seed < 2 {
			for i, o := range ops {
				t.Logf("  输入 op[%d]: %s", i, o)
			}
		}
	}
}

// TestTouchedBounds 证明：Lag 读取事件记录数 <= 64（与 n 无关，实际为 0）；
// Pull 读取数 <= 返回条数 + 跳过条数 + 1。n 取 10^3 与 10^6 两档对照。
func TestTouchedBounds(t *testing.T) {
	for _, n := range []int64{1_000, 1_000_000} {
		svc, err := spectate.New(5000, 10)
		if err != nil {
			t.Fatal(err)
		}
		for i := int64(0); i < n; i++ { // 交替 Normal/Hidden，时刻 0..n-1
			k := live.Normal
			if i%2 == 1 {
				k = live.Hidden
			}
			if _, err := svc.Emit(i, k); err != nil {
				t.Fatal(err)
			}
		}
		if err := svc.Join(n, "v", false); err != nil {
			t.Fatal(err)
		}
		if err := svc.Join(n, "j", true); err != nil {
			t.Fatal(err)
		}
		now := n + 5000 // cutoff = n，全部事件可见（Hidden 赛前不可投递）

		svc.ResetTouched()
		lag, err := svc.Lag(now, "v")
		if err != nil {
			t.Fatal(err)
		}
		if want := (n + 1) / 2; lag != want {
			t.Fatalf("n=%d: Lag = %d, want %d", n, lag, want)
		}
		if touched := svc.Touched(); touched > 64 {
			t.Fatalf("n=%d: Lag touched %d 条事件记录 > 64", n, touched)
		}
		t.Logf("n=%d: Lag=%d 读取事件记录 %d 条（上限 64）", n, lag, svc.Touched())

		// 分批 Pull：交替流下每批跳过数 < 投递数+1，touched == 投递+跳过。
		total := int64(0)
		for {
			svc.ResetTouched()
			out, more, err := svc.Pull(now, "v", 1000)
			if err != nil {
				t.Fatal(err)
			}
			touched := svc.Touched()
			if limit := 2*int64(len(out)) + 1; touched > limit {
				t.Fatalf("n=%d: Pull 投递 %d 条但读取 %d 条记录（> 投递+跳过+1=%d）",
					n, len(out), touched, limit)
			}
			total += int64(len(out))
			if !more {
				break
			}
		}
		if want := (n + 1) / 2; total != want {
			t.Fatalf("n=%d: Pull 共投递 %d, want %d", n, total, want)
		}
		// 空 Pull：至多再越过一条被跳过的 Hidden，之后一条记录也不读。
		svc.ResetTouched()
		if out, _, err := svc.Pull(now, "v", 1000); err != nil || len(out) != 0 {
			t.Fatalf("n=%d: 末尾 Pull = %v, %v", n, out, err)
		}
		if touched := svc.Touched(); touched > 2 { // 0 投递 + 至多 1 跳过 + 1
			t.Fatalf("n=%d: 空 Pull 读取 %d 条记录（> 0+1+1）", n, touched)
		}
		svc.ResetTouched()
		if _, _, err := svc.Pull(now, "v", 1000); err != nil {
			t.Fatal(err)
		}
		if touched := svc.Touched(); touched != 0 {
			t.Fatalf("n=%d: 游标到末尾后的 Pull 读取 %d 条记录", n, touched)
		}
		// 裁判 Pull 无跳过：touched == 投递条数。
		svc.ResetTouched()
		out, _, err := svc.Pull(now, "j", 1000)
		if err != nil {
			t.Fatal(err)
		}
		if touched := svc.Touched(); touched != int64(len(out)) {
			t.Fatalf("n=%d: 裁判 Pull 投递 %d 条但读取 %d 条记录", n, len(out), touched)
		}
		t.Logf("n=%d: Pull 读取记录数满足 <= 投递+跳过+1；裁判批 touched==投递数", n)
	}
}

// TestConcurrentSmoke 并发调用等价于某个串行顺序：客户端时间戳与串行化
// 顺序可能不一致，故 ErrClockRegression 是合法拒绝；其余错误不得出现，
// 且每观战者收到的 seq 严格递增。配合 -race 运行。
func TestConcurrentSmoke(t *testing.T) {
	svc, err := spectate.New(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var now atomic.Int64
	errs := make(chan error, 4096)
	var wg sync.WaitGroup
	accept := func(err error) {
		// 时钟回退与其导致的未加入/不存在均为合法拒绝。
		if err != nil && !errors.Is(err, spectate.ErrClockRegression) &&
			!errors.Is(err, spectate.ErrNotWatching) {
			errs <- err
		}
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				_, err := svc.Emit(now.Add(1), live.Normal)
				accept(err)
			}
		}()
	}
	for g := 0; g < 6; g++ {
		name := fmt.Sprintf("v%d", g)
		wg.Add(1)
		go func() {
			defer wg.Done()
			accept(svc.Join(now.Add(1), name, false))
			last := int64(0)
			for i := 0; i < 150; i++ {
				out, _, err := svc.Pull(now.Add(1), name, 10)
				if err != nil {
					accept(err)
					continue
				}
				for _, ev := range out {
					if ev.Seq <= last {
						errs <- fmt.Errorf("%s 收到非递增 seq %d（上一条 %d）", name, ev.Seq, last)
					}
					last = ev.Seq
				}
				_, err = svc.Lag(now.Add(1), name)
				accept(err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

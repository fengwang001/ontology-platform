package batchisolate

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// simSink 是朴素模拟器与真实实现共用的确定性脚本 sink。
//
// poison：批与毒丸集合相交则永久失败（优先级最高）。
// transient：按有序批内容为 key 的“前 n 次瞬时”计数，之后成功。
type simSink struct {
	poison    map[string]bool
	transient map[string]int
	tries     map[string]int
	calls     int
}

func newSimSink(poison map[string]bool, transient map[string]int) *simSink {
	return &simSink{poison: poison, transient: transient, tries: map[string]int{}}
}

func (s *simSink) write(ids []string) error {
	s.calls++
	for _, id := range ids {
		if s.poison[id] {
			return errPermanent
		}
	}
	key := setKey(ids)
	s.tries[key]++
	if s.tries[key] <= s.transient[key] {
		return ErrTransient
	}
	return nil
}

type simResult struct {
	delivered []string
	dead      []DeadLetter
	calls     int
	known     []string
}

// naiveSim 严格按规格文字逐步翻译的朴素递归模拟器。
// known 为 Submit 开始时读到的已知毒丸表（仅判定 membership）。
// 返回交付/死信（按原序）、调用次数以及 Submit 结束时的新表。
func naiveSim(ids []string, r, km, cmax int, sink *simSink, known []string) simResult {
	snapshot := map[string]struct{}{}
	for _, k := range known {
		snapshot[k] = struct{}{}
	}
	reasons := map[string]Reason{}

	// 摘出已知毒丸，其余保持原序。
	pending := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := snapshot[id]; ok {
			reasons[id] = Known
		} else {
			pending = append(pending, id)
		}
	}

	// 判定顺序记录新毒丸。
	var newPoison []string
	seenNew := map[string]struct{}{}
	budgetAbort := false

	// solve：certain=true 不调用 sink 且 perm=true。
	// 返回 ok（整批成功）；budgetAbort 通过外层变量感知。
	var solve func(ids []string, certain bool) bool
	solve = func(ids []string, certain bool) bool {
		n := len(ids)
		if n == 0 {
			return true
		}
		markBudget := func(batch []string) {
			for _, id := range batch {
				if _, ok := reasons[id]; !ok {
					reasons[id] = Budget
				}
			}
		}
		perm := true
		if !certain {
			success := false
			for try := 0; try <= r; try++ {
				if sink.calls >= cmax {
					budgetAbort = true
					markBudget(ids)
					return false
				}
				err := sink.write(ids)
				if err == nil {
					success = true
					break
				}
				if !isTransient(err) {
					perm = true
					break
				}
				perm = false // 本次为瞬时；若耗尽退出则保持 false
			}
			if success {
				return true
			}
			if budgetAbort {
				return false
			}
		}
		if n == 1 {
			id := ids[0]
			if perm {
				reasons[id] = Poison
				if _, inSnap := snapshot[id]; !inSnap {
					if _, seen := seenNew[id]; !seen {
						seenNew[id] = struct{}{}
						newPoison = append(newPoison, id)
					}
				}
			} else {
				reasons[id] = Exhausted
			}
			return false
		}
		mid := (n + 1) / 2
		left, right := ids[:mid], ids[mid:]
		lok := solve(left, false)
		if budgetAbort {
			// 左分支中止：其未裁决者已就地标记；右半尚未触及，整体 Budget。
			markBudget(right)
			return false
		}
		solve(right, perm && lok)
		return false
	}

	solve(pending, false)

	// 结束时按判定顺序整体并入（FIFO 淘汰，已在表中不刷新）。
	table := append([]string(nil), known...)
	present := map[string]struct{}{}
	for _, k := range table {
		present[k] = struct{}{}
	}
	for _, id := range newPoison {
		if _, ok := present[id]; ok {
			continue
		}
		if km == 0 {
			continue
		}
		if len(table) >= km {
			table = table[1:]
		}
		table = append(table, id)
		present[id] = struct{}{}
	}

	res := simResult{calls: sink.calls, known: table}
	res.delivered = []string{}
	res.dead = []DeadLetter{}
	for _, id := range ids {
		if reason, ok := reasons[id]; ok {
			res.dead = append(res.dead, DeadLetter{ID: id, Reason: reason})
		} else {
			res.delivered = append(res.delivered, id)
		}
	}
	return res
}

func isTransient(err error) bool {
	return err == ErrTransient
}

// genCase 生成一组随机场景。
type genCase struct {
	ids       []string
	poison    map[string]bool
	transient map[string]int
	r, km     int
	cmax      int
	seed      int64
}

func genRandomCase(rng *rand.Rand) genCase {
	c := genCase{
		poison:    map[string]bool{},
		transient: map[string]int{},
		r:         rng.Intn(4), // 0..3
		km:        rng.Intn(6), // 0..5，小容量更容易触发淘汰
		seed:      rng.Int63(),
	}
	n := 1 + rng.Intn(12)
	c.ids = make([]string, n)
	for i := range c.ids {
		c.ids[i] = fmt.Sprintf("i%02d", i)
	}
	// 每个编号独立以概率成为毒丸（保证可能无毒丸）。
	for _, id := range c.ids {
		if rng.Intn(3) == 0 {
			c.poison[id] = true
		}
	}
	// 对若干“潜在批形状”注入瞬时脚本：按递归可能出现的前缀/区间批。
	// 为保持真实与模拟共用同一脚本，只需按有序切片 key 注入；
	// 这里随机给若干区间形状生成前 t 次瞬时（t ≤ R+2，可能超过 R 触发耗尽）。
	shapes := 0
	if n > 1 {
		shapes = rng.Intn(3)
	}
	for s := 0; s < shapes; s++ {
		lo := rng.Intn(n)
		hi := lo + 1 + rng.Intn(n-lo)
		batch := append([]string(nil), c.ids[lo:hi]...)
		// 含毒丸的批在 sink 中永远永久失败，注入瞬时无意义，跳过。
		hasPoison := false
		for _, id := range batch {
			if c.poison[id] {
				hasPoison = true
			}
		}
		if hasPoison {
			continue
		}
		key := setKey(batch)
		if _, dup := c.transient[key]; dup {
			continue
		}
		c.transient[key] = rng.Intn(c.r + 3) // 0..R+2
	}
	// 预算：大多数充足，小概率收紧以触发 Budget。
	if rng.Intn(4) == 0 {
		c.cmax = 1 + rng.Intn(4)
	} else {
		c.cmax = 1000000
	}
	return c
}

// 随机场景排序的稳定 key 列表，便于日志。
func sortedPoison(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestNaiveSimRandom2000(t *testing.T) {
	const total = 2000
	rng := rand.New(rand.NewSource(20261002))
	for iter := 0; iter < total; iter++ {
		c := genRandomCase(rng)

		// 朴素模拟（已知表初始为空）。
		simSink := newSimSink(c.poison, c.transient)
		sim := naiveSim(c.ids, c.r, c.km, c.cmax, simSink, nil)

		// 真实实现：包一层 SinkFunc 复用同一脚本。
		realSink := newSimSink(c.poison, c.transient)
		iso, err := New(c.r, c.km, c.cmax, realSink.write)
		if err != nil {
			t.Fatalf("iter %d: New: %v", iter, err)
		}
		res, err := iso.Submit(c.ids)
		if err != nil {
			t.Fatalf("iter %d: Submit: %v", iter, err)
		}

		if fmt.Sprint(res.Delivered) != fmt.Sprint(sim.delivered) {
			t.Fatalf("iter %d seed=%d case=%+v\n delivered got=%v\nwant=%v",
				iter, c.seed, c, res.Delivered, sim.delivered)
		}
		if fmt.Sprint(res.Dead) != fmt.Sprint(sim.dead) {
			t.Fatalf("iter %d seed=%d case=%+v\n dead got=%v\nwant=%v",
				iter, c.seed, c, res.Dead, sim.dead)
		}
		if res.Calls != sim.calls {
			t.Fatalf("iter %d seed=%d calls got=%d want=%d case=%+v",
				iter, c.seed, res.Calls, sim.calls, c)
		}
		if got := iso.Known(); fmt.Sprint(got) != fmt.Sprint(sim.known) {
			t.Fatalf("iter %d seed=%d known got=%v want=%v",
				iter, c.seed, got, sim.known)
		}

		if iter < 10 || iter%200 == 0 {
			t.Logf("iter=%d seed=%d 输入=%v 毒丸=%v R=%d Km=%d Cmax=%d | 交付=%v 死信=%v Calls=%d 表=%v | 判定与朴素模拟一致",
				iter, c.seed, c.ids, sortedPoison(c.poison), c.r, c.km, c.cmax,
				res.Delivered, res.Dead, res.Calls, iso.Known())
		}
	}
	t.Logf("完成 %d 组随机批/毒丸/瞬时脚本与朴素递归模拟对照，全部一致", total)
}

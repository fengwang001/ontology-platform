package matcher

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// naive 是按需求规则逐步写成的朴素模拟，用作对照实现。
type naive struct {
	lastHour int64
	commits  []naiveCommit
}

type naiveCommit struct {
	id, fam, start, n, h, up, d int64
}

func newNaive() *naive { return &naive{lastHour: -1} }

func (nv *naive) addCommit(id, fam, start, n, h, up, d int64) error {
	if id < 0 || id > 1e6 || fam < 0 || fam > 99 || start < 0 ||
		n < 1 || n > 1e5 || h < 1 || h > 1e9 || up < 0 || up > 1e12 || d < 1 || d > 9999 {
		return ErrInvalidArgument
	}
	for _, c := range nv.commits {
		if c.id == id {
			return ErrDuplicateID
		}
	}
	if start <= nv.lastHour {
		return ErrStartPassed
	}
	nv.commits = append(nv.commits, naiveCommit{id, fam, start, n, h, up, d})
	return nil
}

func (nv *naive) apply(t int64, lines []Line) ([]HourReport, error) {
	if t < 0 || t > nv.lastHour+10000 || len(lines) > 1000 {
		return nil, ErrInvalidArgument
	}
	for _, ln := range lines {
		if ln.Fam < 1 || ln.Fam > 99 || ln.P < 1 || ln.P > 1e9 {
			return nil, ErrInvalidArgument
		}
	}
	if t <= nv.lastHour {
		return nil, ErrTimeRegression
	}
	var out []HourReport
	for hour := nv.lastHour + 1; hour <= t; hour++ {
		var ls []Line
		if hour == t {
			ls = lines
		}
		out = append(out, nv.runHour(hour, ls))
	}
	nv.lastHour = t
	return out, nil
}

func (nv *naive) runHour(hour int64, lines []Line) HourReport {
	var act []naiveCommit
	for _, c := range nv.commits {
		if c.start <= hour && hour < c.start+c.n {
			act = append(act, c)
		}
	}
	sort.Slice(act, func(i, j int) bool {
		if act[i].d != act[j].d {
			return act[i].d > act[j].d
		}
		return act[i].id < act[j].id
	})
	rem := make([]int64, len(lines))
	for i, ln := range lines {
		rem[i] = ln.P
	}
	rep := HourReport{Hour: hour}
	for _, c := range act {
		r := c.h
		m := int64(10000) - c.d
		var cov int64
		for i := range lines {
			if r == 0 {
				break
			}
			if c.fam != 0 && c.fam != lines[i].Fam {
				continue
			}
			p := rem[i]
			if p == 0 {
				continue
			}
			e := (p*m + 9999) / 10000
			if r >= e {
				r -= e
				cov += p
				rem[i] = 0
			} else {
				q := r * 10000 / m
				cov += q
				rem[i] = p - q
				r = 0
			}
		}
		rep.Commits = append(rep.Commits, CommitReport{ID: c.id, Used: c.h - r, Unused: r, Covered: cov})
		base := c.up / c.n
		amort := base
		if hour-c.start == c.n-1 {
			amort = c.up - (c.n-1)*base
		}
		rep.Bill += c.h + amort
	}
	for _, p := range rem {
		rep.Bill += p
	}
	return rep
}

// op 是一步随机操作：add 或 apply。
type op struct {
	isAdd bool
	args  [7]int64
	t     int64
	lines []Line
}

func (o op) String() string {
	if o.isAdd {
		return fmt.Sprintf("AddCommit%v", o.args)
	}
	return fmt.Sprintf("Apply(%d,%v)", o.t, o.lines)
}

// genOps 生成一条随机操作序列（含少量非法操作）。
func genOps(rng *rand.Rand) []op {
	var ops []op
	lastHour := int64(-1)
	n := 1 + rng.Intn(30)
	for i := 0; i < n; i++ {
		if rng.Intn(2) == 0 {
			o := op{isAdd: true, args: [7]int64{
				int64(rng.Intn(12)),           // 小 id 池，制造重复
				int64(rng.Intn(4)),            // fam 0..3
				lastHour + int64(rng.Intn(4)), // start 在 lastHour 附近
				1 + int64(rng.Intn(6)),        // n 1..6
				1 + int64(rng.Intn(200)),      // h
				int64(rng.Intn(500)),          // up
				int64(1 + rng.Intn(9999)),     // d
			}}
			if rng.Intn(4) == 0 {
				o.args[6] = int64(1 + rng.Intn(3)) // 小 d 池，制造并列
			}
			switch rng.Intn(10) {
			case 0:
				o.args[rng.Intn(7)] = -1 // 注入非法参数
			case 1:
				o.args[3] = 1e5 + 1
			}
			ops = append(ops, o)
		} else {
			o := op{t: lastHour + 1 + int64(rng.Intn(4))}
			switch rng.Intn(12) {
			case 0:
				o.t = lastHour // 时间回退
			case 1:
				o.t = lastHour + 10001 // 超出前跳上限
			case 2:
				o.t = -1
			}
			nl := rng.Intn(5)
			for j := 0; j < nl; j++ {
				ln := Line{Fam: 1 + int64(rng.Intn(4)), P: 1 + int64(rng.Intn(300))}
				if rng.Intn(15) == 0 {
					ln.Fam = 0 // 非法族
				}
				if rng.Intn(15) == 0 {
					ln.P = 0 // 非法按需价
				}
				o.lines = append(o.lines, ln)
			}
			ops = append(ops, o)
			if o.t > lastHour && o.t <= lastHour+10000 {
				bad := false
				for _, ln := range o.lines {
					if ln.Fam < 1 || ln.Fam > 99 || ln.P < 1 || ln.P > 1e9 {
						bad = true
					}
				}
				if !bad {
					lastHour = o.t
				}
			}
		}
	}
	return ops
}

// runOps 在 Matcher 上重放操作序列；splitApply 为 true 时把每次 Apply
// 拆成逐小时调用（验证跳过小时与逐小时处理等价）。
func runOps(m *Matcher, ops []op, splitApply bool) ([]interface{}, error) {
	var out []interface{}
	lastHour := int64(-1)
	for _, o := range ops {
		if o.isAdd {
			err := m.AddCommit(o.args[0], o.args[1], o.args[2], o.args[3], o.args[4], o.args[5], o.args[6])
			out = append(out, err)
			continue
		}
		// 仅在原调用必然成功（参数全部合法）时才逐小时拆分，
		// 否则退化为单次调用，保证被拒绝的 Apply 输出与状态完全一致。
		linesOK := len(o.lines) <= 1000
		for _, ln := range o.lines {
			if ln.Fam < 1 || ln.Fam > 99 || ln.P < 1 || ln.P > 1e9 {
				linesOK = false
			}
		}
		if !splitApply || !linesOK || o.t-1 <= lastHour || o.t > lastHour+10000 {
			reps, err := m.Apply(o.t, o.lines)
			if err != nil {
				out = append(out, err)
			} else {
				out = append(out, reps)
				lastHour = o.t
			}
			continue
		}
		// 拆分：先逐小时空行推进到 t-1，再带 lines 处理 t。
		reps, err := m.Apply(o.t-1, nil)
		if err != nil {
			out = append(out, err)
			continue
		}
		out = append(out, reps)
		lastHour = o.t - 1
		reps, err = m.Apply(o.t, o.lines)
		if err != nil {
			out = append(out, err)
		} else {
			out = append(out, reps)
			lastHour = o.t
		}
	}
	return out, nil
}

// flatten 把输出序列展平为可比较的统一形式。
func flatten(out []interface{}) []interface{} {
	var flat []interface{}
	for _, v := range out {
		if reps, ok := v.([]HourReport); ok {
			for _, r := range reps {
				flat = append(flat, r)
			}
		} else {
			flat = append(flat, v)
		}
	}
	return flat
}

// checkInvariants 校验每小时的守恒不变量，返回失败原因。
func checkInvariants(rep HourReport, commits map[int64][2]int64) string {
	for _, cr := range rep.Commits {
		hc, ok := commits[cr.ID]
		if !ok {
			return fmt.Sprintf("hour %d: 报告中出现未登记的承诺 id=%d", rep.Hour, cr.ID)
		}
		if cr.Used+cr.Unused != hc[0] {
			return fmt.Sprintf("hour %d commit %d: used+unused=%d != h=%d",
				rep.Hour, cr.ID, cr.Used+cr.Unused, hc[0])
		}
		if cr.Used < 0 || cr.Unused < 0 || cr.Covered < 0 {
			return fmt.Sprintf("hour %d commit %d: 出现负值 %+v", rep.Hour, cr.ID, cr)
		}
	}
	return ""
}

func digest(out []interface{}) uint64 {
	h := fnv.New64a()
	for _, v := range out {
		fmt.Fprintf(h, "%#v;", v)
	}
	return h.Sum64()
}

// TestRandomAgainstNaive 用 2000 组随机序列与朴素模拟对照，
// 并验证跳过小时与逐小时 Apply 等价、重放结果确定。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		ops := genOps(rng)

		// 被测实现：按原样执行。
		m1 := New()
		out1, _ := runOps(m1, ops, false)
		// 被测实现：Apply 拆成逐小时调用。
		m2 := New()
		out2, _ := runOps(m2, ops, true)
		// 朴素模拟对照。
		nv := newNaive()
		var outRef []interface{}
		for _, o := range ops {
			if o.isAdd {
				outRef = append(outRef, nv.addCommit(o.args[0], o.args[1], o.args[2], o.args[3], o.args[4], o.args[5], o.args[6]))
			} else {
				reps, err := nv.apply(o.t, o.lines)
				if err != nil {
					outRef = append(outRef, err)
				} else {
					outRef = append(outRef, reps)
				}
			}
		}

		flat1, flat2, flatRef := flatten(out1), flatten(out2), flatten(outRef)
		ok := reflect.DeepEqual(flat1, flatRef)
		splitOK := reflect.DeepEqual(flat1, flat2)

		// 不变量校验：used+unused==h，值非负。承诺表取 m1 中登记成功的承诺。
		commits := map[int64][2]int64{}
		for i, o := range ops {
			if o.isAdd && out1[i] == nil {
				commits[o.args[0]] = [2]int64{o.args[4], o.args[5]}
			}
		}
		invErr := ""
		for _, v := range flat1 {
			switch x := v.(type) {
			case HourReport:
				if s := checkInvariants(x, commits); s != "" && invErr == "" {
					invErr = s
				}
			}
		}
		// 重放确定性：相同操作序列在新实例上得到相同报告。
		m3 := New()
		out3, _ := runOps(m3, ops, false)
		replayOK := reflect.DeepEqual(flat1, flatten(out3))

		t.Logf("seq=%d 输入=%v", seq, ops)
		t.Logf("seq=%d 输出摘要: matcher=%x split=%x naive=%x", seq, digest(flat1), digest(flat2), digest(flatRef))
		t.Logf("seq=%d 判定依据: 与朴素模拟逐字段DeepEqual=%v, 逐小时拆分等价=%v, 重放确定=%v, 不变量=%q",
			seq, ok, splitOK, replayOK, invErr)

		if !ok || !splitOK || !replayOK || invErr != "" {
			t.Fatalf("seq=%d 失败: deepEqual=%v split=%v replay=%v invariant=%q\n输入=%v\nmatcher=%v\nsplit=%v\nnaive=%v",
				seq, ok, splitOK, replayOK, invErr, ops, flat1, flat2, flatRef)
		}
	}
}

// TestConcurrent 并发调用 AddCommit/Apply：结果必须等价于某个串行顺序。
// 判定依据：每个小时在全部成功的 Apply 中恰好被报告一次；
// 相同 id 的并发 AddCommit 恰好成功一次。
func TestConcurrent(t *testing.T) {
	m := New()
	const workers = 16
	const hours = 200

	var addOK int64
	var applyOK int64
	var mu sync.Mutex
	reported := make([]int64, hours) // 每小时被报告的次数

	var wg sync.WaitGroup
	// 一半 goroutine 并发登记相同 id 的承诺。
	for w := 0; w < workers/2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := int64(i % 10) // 小 id 池，制造并发重复
				// start 取测试时域之后，避免与并发推进的 lastHour 竞争。
				if err := m.AddCommit(id, 0, hours, 1, 10, 30, 1000); err == nil {
					atomic.AddInt64(&addOK, 1)
				}
			}
		}(w)
	}
	// 另一半并发推进小时。
	var next atomic.Int64
	for w := 0; w < workers/2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				h := next.Add(1) - 1
				if h >= hours {
					return
				}
				reps, err := m.Apply(h, []Line{{Fam: 1, P: 5}})
				if err != nil {
					continue // 串行化后该小时已被别人处理
				}
				atomic.AddInt64(&applyOK, 1)
				mu.Lock()
				for _, r := range reps {
					reported[r.Hour]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if addOK != 10 {
		t.Errorf("成功的 AddCommit 数 = %d, want 10 (每个 id 恰好一次)", addOK)
	}
	for h := 0; h < hours; h++ {
		if reported[h] != 1 {
			t.Fatalf("hour %d 被报告 %d 次, want 1", h, reported[h])
		}
	}
	t.Logf("并发判定依据: 每个 id 恰好登记一次(=%d), 每小时恰好报告一次(0..%d)", addOK, hours-1)
}

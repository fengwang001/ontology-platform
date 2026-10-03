package retry

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 朴素模拟：按规格逐条直译的独立实现，用于与 Scheduler 对拍。
// 刻意采用不同的代码结构（逐语句展开、循环算 2 的幂），避免与实现共享逻辑。

type modelTask struct {
	chain    []string
	deadline int64
	cur      int
	n        int64
	att      int64
	nextAt   int64
	done     bool
	success  bool
	reason   DeadReason
}

type model struct {
	cfg    Config
	jitter JitterFunc
	tasks  map[string]*modelTask
	gfail  map[string]int64
	open   map[string]int64
	maxNow int64
}

func newModel(cfg Config, j JitterFunc) *model {
	return &model{
		cfg: cfg, jitter: j,
		tasks: make(map[string]*modelTask),
		gfail: make(map[string]int64),
		open:  make(map[string]int64),
	}
}

func (m *model) submit(id string, chain []string, created, ttl int64) error {
	if id == "" || len(chain) < 1 || len(chain) > 4 ||
		created < 0 || created > maxTime || ttl < 1 || ttl > 1_000_000_000 {
		return ErrInvalidParam
	}
	seen := map[string]bool{}
	for _, ch := range chain {
		if ch == "" || seen[ch] {
			return ErrInvalidParam
		}
		seen[ch] = true
	}
	if _, dup := m.tasks[id]; dup {
		return ErrDuplicate
	}
	cp := append([]string(nil), chain...)
	m.tasks[id] = &modelTask{chain: cp, deadline: created + ttl, nextAt: created}
	return nil
}

// 返回结果、错误与判定依据（用于日志）。
func (m *model) fail(id string, now int64, class Class, ra int64) (Result, error, string) {
	if class != Transient && class != Throttled && class != Permanent {
		return Result{}, ErrInvalidParam, "reject: 非法 class"
	}
	if ra < 0 || ra > 1_000_000_000 {
		return Result{}, ErrInvalidParam, "reject: ra 越界"
	}
	if class != Throttled && ra != 0 {
		return Result{}, ErrInvalidParam, "reject: 非 throttled 的 ra 非零"
	}
	if now < 0 || now > maxTime {
		return Result{}, ErrInvalidParam, "reject: now 越界"
	}
	tk, ok := m.tasks[id]
	if !ok {
		return Result{}, ErrNotFound, "reject: 任务不存在"
	}
	if tk.done {
		return Result{}, ErrFinished, "reject: 任务已结束"
	}
	if now < m.maxNow {
		return Result{}, ErrClockBackwards, fmt.Sprintf("reject: 时钟回退 now=%d < maxNow=%d", now, m.maxNow)
	}
	if now < tk.nextAt {
		return Result{}, ErrTooEarly, fmt.Sprintf("reject: 过早 now=%d < nextAt=%d", now, tk.nextAt)
	}
	m.maxNow = now

	// (1) att 加一
	tk.att++
	ch := tk.chain[tk.cur]
	// (2) 非 permanent 累计 gfail，达到 K 熔断
	if class != Permanent {
		m.gfail[ch]++
		if m.gfail[ch] >= m.cfg.K {
			m.open[ch] = now + m.cfg.Cool
		}
	}
	// (3) 判定等待或切换
	var w int64
	var reason DeadReason
	sw := false
	basis := ""
	if class == Permanent {
		sw, reason = true, ReasonPermanent
		basis = "permanent 进入切换分支"
	} else if class == Throttled && ra > m.cfg.RAcap {
		sw, reason = true, ReasonThrottled
		basis = fmt.Sprintf("ra=%d > RAcap=%d 进入切换分支", ra, m.cfg.RAcap)
	} else {
		tk.n++
		if tk.n >= m.cfg.M {
			sw, reason = true, ReasonExhausted
			basis = fmt.Sprintf("n=%d 达到 M=%d 进入切换分支", tk.n, m.cfg.M)
		} else {
			d := m.cfg.Base
			for i := int64(1); i < tk.n; i++ {
				d *= 2
			}
			if d > m.cfg.Cap {
				d = m.cfg.Cap
			}
			j := m.jitter(id, tk.n, d)
			jc := j
			if jc < 0 {
				jc = 0
			}
			if jc > d/4 {
				jc = d / 4
			}
			dp := d - jc
			w = dp
			if ra > w {
				w = ra
			}
			basis = fmt.Sprintf("n=%d d=%d jitter=%d 夹为 %d d'=%d ra=%d w=%d", tk.n, d, j, jc, dp, ra, w)
		}
	}
	// (4) 切换分支
	if sw {
		found := -1
		for i := tk.cur + 1; i < len(tk.chain); i++ {
			if m.open[tk.chain[i]] <= now {
				found = i
				break
			}
		}
		if found < 0 {
			tk.done, tk.n, tk.reason = true, 0, reason
			return Result{Dead: true, Reason: reason}, nil,
				basis + fmt.Sprintf("; 无可用后续渠道, 死信 %s", reason)
		}
		tk.cur, tk.n, w = found, 0, 0
		basis += fmt.Sprintf("; 切换到 %s", tk.chain[found])
	}
	// (5) 预算
	if tk.att >= m.cfg.A {
		tk.done, tk.n, tk.reason = true, 0, ReasonBudget
		return Result{Dead: true, Reason: ReasonBudget}, nil,
			basis + fmt.Sprintf("; att=%d 达到 A=%d, 死信 budget", tk.att, m.cfg.A)
	}
	// (6) 截止
	tk.nextAt = now + w
	if tk.nextAt > tk.deadline {
		tk.done, tk.n, tk.reason = true, 0, ReasonExpired
		return Result{Dead: true, Reason: ReasonExpired}, nil,
			basis + fmt.Sprintf("; nextAt=%d > deadline=%d, 死信 expired", tk.nextAt, tk.deadline)
	}
	return Result{Channel: tk.chain[tk.cur], NextAt: tk.nextAt}, nil,
		basis + fmt.Sprintf("; 返回 (%s, %d)", tk.chain[tk.cur], tk.nextAt)
}

func (m *model) success(id string, now int64) (error, string) {
	if now < 0 || now > maxTime {
		return ErrInvalidParam, "reject: now 越界"
	}
	tk, ok := m.tasks[id]
	if !ok {
		return ErrNotFound, "reject: 任务不存在"
	}
	if tk.done {
		return ErrFinished, "reject: 任务已结束"
	}
	if now < m.maxNow {
		return ErrClockBackwards, "reject: 时钟回退"
	}
	if now < tk.nextAt {
		return ErrTooEarly, "reject: 过早"
	}
	m.maxNow = now
	tk.done, tk.success = true, true
	m.gfail[tk.chain[tk.cur]] = 0
	return nil, fmt.Sprintf("完成, gfail[%s] 清零", tk.chain[tk.cur])
}

// 随机操作序列与朴素模拟对拍：2000 组，逐操作比较回报，并校验终态一致。
func TestModelFuzz(t *testing.T) {
	const sequences = 2000
	channels := []string{"push", "sms", "email", "webhook", "voice"}
	classes := []Class{Transient, Transient, Transient, Throttled, Throttled, Permanent}

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		cfg := Config{
			Base:  1 + rng.Int63n(20),
			M:     1 + rng.Int63n(6),
			A:     1 + rng.Int63n(12),
			RAcap: rng.Int63n(60),
			K:     1 + rng.Int63n(4),
			Cool:  1 + rng.Int63n(80),
		}
		cfg.Cap = cfg.Base + rng.Int63n(200)
		// 确定性抖动：纯函数，覆盖负数、界内、超界。
		seed := rng.Int63()
		jitter := func(id string, n, d int64) int64 {
			h := seed
			for _, c := range id {
				h = h*31 + int64(c)
			}
			h = h*6364136223846793005 + n*1442695040888963407 + d
			h ^= h >> 33
			span := d/2 + 2
			return h%(2*span) - span // 落在 [-d/2-1, d/2]，必然触及上下夹取
		}

		s, err := New(cfg, jitter)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		s2, _ := New(cfg, jitter) // 重放用第二个实例
		m := newModel(cfg, jitter)

		clock := rng.Int63n(50)
		ids := []string{}
		ops := 20 + rng.Intn(30)
		for op := 0; op < ops; op++ {
			clock += rng.Int63n(25)
			now := clock
			if rng.Intn(10) == 0 { // 偶发时钟回退
				now = clock - rng.Int63n(10)
			}
			log := func(format string, args ...interface{}) {
				t.Logf(fmt.Sprintf("seq=%d op=%d ", seq, op)+format, args...)
			}
			switch rng.Intn(3) {
			case 0: // Submit
				id := fmt.Sprintf("id%d", rng.Intn(8))
				size := 1 + rng.Intn(4)
				perm := rng.Perm(len(channels))[:size]
				chain := make([]string, size)
				for i, p := range perm {
					chain[i] = channels[p]
				}
				if rng.Intn(20) == 0 && size > 1 { // 偶发非法：重复渠道
					chain[1] = chain[0]
				}
				ttl := int64(1 + rng.Intn(400))
				err1 := s.Submit(id, chain, now, ttl)
				err2 := m.submit(id, chain, now, ttl)
				if err3 := s2.Submit(id, chain, now, ttl); !sameErr(err1, err3) {
					t.Fatalf("seq %d op %d: replay mismatch %v vs %v", seq, op, err1, err3)
				}
				log("Submit(%s, %v, now=%d, ttl=%d) -> %v | 模型 %v", id, chain, now, ttl, err1, err2)
				if !sameErr(err1, err2) {
					t.Fatalf("seq %d op %d Submit: got %v, model %v", seq, op, err1, err2)
				}
				if err1 == nil {
					ids = append(ids, id)
				}
			case 1: // Fail
				id := pickID(rng, ids)
				class := classes[rng.Intn(len(classes))]
				var ra int64
				if class == Throttled && rng.Intn(2) == 0 {
					ra = rng.Int63n(cfg.RAcap + 3)
				}
				if rng.Intn(15) == 0 {
					ra++ // 偶发非法 ra
				}
				r1, e1 := s.Fail(id, now, class, ra)
				r2, e2, basis := m.fail(id, now, class, ra)
				r3, e3 := s2.Fail(id, now, class, ra)
				log("Fail(%s, now=%d, %s, ra=%d) -> %+v %v | 模型 %+v %v | 依据: %s",
					id, now, class, ra, r1, e1, r2, e2, basis)
				if r1 != r2 || !sameErr(e1, e2) {
					t.Fatalf("seq %d op %d Fail(%s,%d,%s,%d): got (%+v,%v), model (%+v,%v)",
						seq, op, id, now, class, ra, r1, e1, r2, e2)
				}
				if r1 != r3 || !sameErr(e1, e3) {
					t.Fatalf("seq %d op %d: replay mismatch", seq, op)
				}
			case 2: // Success
				id := pickID(rng, ids)
				e1 := s.Success(id, now)
				e2, basis := m.success(id, now)
				e3 := s2.Success(id, now)
				log("Success(%s, now=%d) -> %v | 模型 %v | 依据: %s", id, now, e1, e2, basis)
				if !sameErr(e1, e2) {
					t.Fatalf("seq %d op %d Success(%s,%d): got %v, model %v", seq, op, id, now, e1, e2)
				}
				if !sameErr(e1, e3) {
					t.Fatalf("seq %d op %d: replay mismatch", seq, op)
				}
			}
		}

		// 终态一致：任务表、熔断计数、熔断窗口、最大时钟。
		if len(s.tasks) != len(m.tasks) {
			t.Fatalf("seq %d: task count %d vs model %d", seq, len(s.tasks), len(m.tasks))
		}
		for id, tk := range s.tasks {
			mt := m.tasks[id]
			if mt == nil {
				t.Fatalf("seq %d: task %s missing in model", seq, id)
			}
			if tk.cur != mt.cur || tk.n != mt.n || tk.att != mt.att ||
				tk.nextAt != mt.nextAt || tk.done != mt.done ||
				tk.reason != mt.reason || tk.success != mt.success ||
				tk.deadline != mt.deadline {
				t.Fatalf("seq %d: task %s state %+v vs model %+v", seq, id, tk, mt)
			}
		}
		if !reflect.DeepEqual(s.gfail, m.gfail) {
			t.Fatalf("seq %d: gfail %v vs model %v", seq, s.gfail, m.gfail)
		}
		if !reflect.DeepEqual(s.openUntil, m.open) {
			t.Fatalf("seq %d: openUntil %v vs model %v", seq, s.openUntil, m.open)
		}
		if s.maxNow != m.maxNow {
			t.Fatalf("seq %d: maxNow %d vs model %d", seq, s.maxNow, m.maxNow)
		}
	}
}

func pickID(rng *rand.Rand, ids []string) string {
	if len(ids) == 0 || rng.Intn(10) == 0 {
		return fmt.Sprintf("id%d", rng.Intn(8)) // 可能是幽灵 id
	}
	return ids[rng.Intn(len(ids))]
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a == b
}

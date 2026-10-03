package regex

import "sync"

// 参数与输入的合法范围。
const (
	MaxIDLen      = 64
	MaxPatternLen = 200
	MaxInputLen   = 65536
	MaxNow        = int64(1_000_000_000_000_000) // 10^15
)

// Outcome 为一次被接受的 Match 的结果类别。
type Outcome int

const (
	OutcomeMatch       Outcome = iota // 匹配成功
	OutcomeNoMatch                    // 全部起点失败
	OutcomeLocalLimit                 // 本地超限（rem' >= L，λ = L）
	OutcomeGlobalLimit                // 全局超限（rem' < L，λ = rem'）
)

func (o Outcome) String() string {
	switch o {
	case OutcomeMatch:
		return "Match"
	case OutcomeNoMatch:
		return "NoMatch"
	case OutcomeLocalLimit:
		return "LocalLimit"
	case OutcomeGlobalLimit:
		return "GlobalLimit"
	}
	return "Unknown"
}

// MatchResult 为被接受的 Match 的返回值；Start/End 仅在 OutcomeMatch 时有效。
type MatchResult struct {
	Outcome Outcome
	Start   int
	End     int
	Steps   int64
}

// Status 为模式在某一时刻的状态快照。
type Status struct {
	C      int64 // 连续本地超限计数
	B      int64 // 累计封禁次数
	U      int64 // 封禁截止时刻
	Banned bool  // now < U
}

type pattern struct {
	prog []inst
	memo bool
	c    int64
	b    int64
	u    int64
}

// Engine 为回溯正则执行器。所有方法可并发调用，效果等价于某个串行顺序。
type Engine struct {
	mu sync.Mutex

	l int64 // 本地步数上限 L，1..10^6
	e int64 // 纪元长度 E，1..10^9
	g int64 // 每纪元全局步数 G，1..10^9
	k int64 // 封禁触发次数 K，1..100
	d int64 // 基础封禁时长 D，1..10^9
	p int64 // 程序大小上限 P，1..10^5

	maxNow int64 // 已被接受的 Match 见过的最大 now，初 0
	epoch  int64 // 当前纪元，初 0
	rem    int64 // 当前纪元全局余量，初 G

	pats map[string]*pattern

	dispatched int64 // 非导出计数器：已接受的 Match 的实际调度总数
}

// NewEngine 构造执行器；任一参数越界返回 ErrInvalidArgs。
func NewEngine(l, e, g, k, d, p int64) (*Engine, error) {
	if l < 1 || l > 1_000_000 ||
		e < 1 || e > 1_000_000_000 ||
		g < 1 || g > 1_000_000_000 ||
		k < 1 || k > 100 ||
		d < 1 || d > 1_000_000_000 ||
		p < 1 || p > 100_000 {
		return nil, ErrInvalidArgs
	}
	return &Engine{
		l: l, e: e, g: g, k: k, d: d, p: p,
		rem:  g,
		pats: make(map[string]*pattern),
	}, nil
}

// Register 注册模式。拒绝次序：参数非法、id 已存在、语法错误、可空重复、程序过大。
// 被拒绝时不改变任何状态。
func (e *Engine) Register(id string, pat string, memoize bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(id) == 0 || len(id) > MaxIDLen || len(pat) == 0 || len(pat) > MaxPatternLen {
		return ErrInvalidArgs
	}
	if _, ok := e.pats[id]; ok {
		return ErrAlreadyExists
	}
	ast, err := parse(pat)
	if err != nil {
		return err
	}
	if hasNullableRepeat(ast) {
		return ErrNullableRepeat
	}
	prog, err := compile(ast, e.p)
	if err != nil {
		return err
	}
	e.pats[id] = &pattern{prog: prog, memo: memoize}
	return nil
}

// Match 在输入上搜索首个匹配。拒绝次序（只报第一个）：
// 参数非法、模式未注册、时钟回退、模式封禁中、全局余量为 0。
// 被拒绝时不改变任何状态；只有被接受的 Match 才推进最大 now、纪元与 rem。
func (e *Engine) Match(id string, input []byte, now int64) (MatchResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(id) == 0 || len(id) > MaxIDLen || len(input) > MaxInputLen || now < 0 || now > MaxNow {
		return MatchResult{}, ErrInvalidArgs
	}
	pat, ok := e.pats[id]
	if !ok {
		return MatchResult{}, ErrNotRegistered
	}
	if now < e.maxNow {
		return MatchResult{}, ErrClockRegression
	}
	if now < pat.u {
		return MatchResult{}, ErrBanned
	}
	epoch := now / e.e
	remP := e.rem
	if epoch != e.epoch {
		remP = e.g
	}
	if remP == 0 {
		return MatchResult{}, ErrGlobalBudgetExhausted
	}
	lambda := e.l
	local := true
	if remP < e.l {
		lambda = remP
		local = false
	}
	res := search(pat.prog, input, lambda, pat.memo)
	// 提交状态：推进最大 now、纪元与余量。
	e.maxNow = now
	if epoch != e.epoch {
		e.epoch = epoch
		e.rem = e.g
	}
	e.rem -= res.steps
	e.dispatched += res.steps
	out := MatchResult{Steps: res.steps}
	switch {
	case res.matched:
		out.Outcome = OutcomeMatch
		out.Start, out.End = res.start, res.end
		pat.c = 0
	case !res.limited:
		out.Outcome = OutcomeNoMatch
		pat.c = 0
	case local:
		out.Outcome = OutcomeLocalLimit
		pat.c++
		if pat.c >= e.k {
			pat.b++
			mult := int64(8)
			if pat.b < 4 {
				mult = int64(1) << (pat.b - 1)
			}
			pat.u = now + e.d*mult
			pat.c = 0
		}
	default:
		out.Outcome = OutcomeGlobalLimit // 全局超限不改 c
	}
	return out, nil
}

// Status 返回模式在时刻 now 的 c、b、u 与是否封禁中；为纯查询，不改变状态。
func (e *Engine) Status(id string, now int64) (Status, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(id) == 0 || len(id) > MaxIDLen || now < 0 || now > MaxNow {
		return Status{}, ErrInvalidArgs
	}
	pat, ok := e.pats[id]
	if !ok {
		return Status{}, ErrNotRegistered
	}
	return Status{C: pat.c, B: pat.b, U: pat.u, Banned: now < pat.u}, nil
}

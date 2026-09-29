package termination

import (
	"fmt"
	"sync"
)

// Logger 记录每次操作的输入、输出与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

// Detector 是令牌环终止检测器。所有公开操作均为并发安全：
// 检测器内部以单一互斥锁串行化状态变更，网络在锁内被调用，
// 保证“发送加一、接收减一”与令牌判定的原子一致性。
type Detector struct {
	mu       sync.Mutex
	n        int
	net      Network
	log      Logger
	states   []State
	colors   []Color
	counts   []int
	holder   int   // 令牌持有者
	token    Color // 令牌颜色
	accum    int   // 令牌累计值
	round    int   // 当前轮次；0 表示首轮尚未发起
	inFlight bool  // 当前轮令牌是否已离开发起者、尚未回到发起者
	nextID   uint64
	done     chan struct{}
	endRound int // 宣告终止时的轮次（0 表示尚未宣告）
}

// Option 配置检测器。
type Option func(*Detector)

// WithNetwork 注入自定义网络；默认使用 NewMemoryNetwork。
func WithNetwork(net Network) Option {
	return func(d *Detector) { d.net = net }
}

// WithLogger 注入日志记录器；默认不输出。
func WithLogger(l Logger) Option {
	return func(d *Detector) { d.log = l }
}

// New 创建 n 个进程的检测器，0 号进程为发起者。
// 进程初始均为活跃、白色、计数 0；白色令牌由 0 号持有，首轮待发起。
func New(n int, opts ...Option) (*Detector, error) {
	if n <= 0 {
		return nil, ErrInvalidN
	}
	d := &Detector{
		n:      n,
		net:    NewMemoryNetwork(),
		states: make([]State, n),
		colors: make([]Color, n),
		counts: make([]int, n),
		holder: 0,
		done:   make(chan struct{}),
	}
	for i := range n {
		d.states[i] = Active
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.log == nil {
		d.log = discardLogger{}
	}
	d.logf("input New(n=%d) output ok; all processes Active/White, token White held by 0, round not started yet", n)
	return d, nil
}

// Send 仅活跃进程可调用：计数器加一并向网络注入一条消息。
func (d *Detector) Send(from, to int) (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.endRound != 0 {
		return 0, ErrTerminated
	}
	if outOfRange(d.n, from) || outOfRange(d.n, to) {
		d.logf("input Send(from=%d,to=%d) output reject %v", from, to, ErrProcessOutOfRange)
		return 0, ErrProcessOutOfRange
	}
	if from == to {
		d.logf("input Send(from=%d,to=%d) output reject %v", from, to, ErrSendToSelf)
		return 0, ErrSendToSelf
	}
	if d.states[from] == Idle {
		d.logf("input Send(from=%d,to=%d) output reject %v", from, to, ErrIdleSender)
		return 0, ErrIdleSender
	}
	d.nextID++
	id := d.nextID
	msg := Message{ID: id, From: from, To: to}
	d.counts[from]++
	d.net.Inject(msg)
	d.logf("input Send(from=%d,to=%d) output msg#%d injected; count[%d]=%d pending=%d",
		from, to, id, from, d.counts[from], len(d.net.Pending()))
	return id, nil
}

// Deliver 投递网络中指定的消息：接收者计数器减一、变活跃且变黑。
func (d *Detector) Deliver(id uint64) (Message, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.endRound != 0 {
		return Message{}, ErrTerminated
	}
	msg, err := d.net.Remove(id)
	if err != nil {
		d.logf("input Deliver(id=%d) output reject %v", id, err)
		return Message{}, err
	}
	d.counts[msg.To]--
	d.states[msg.To] = Active
	d.colors[msg.To] = Black
	d.logf("input Deliver(id=%d, %d->%d) output received; count[%d]=%d state=Active color=Black pending=%d",
		id, msg.From, msg.To, msg.To, d.counts[msg.To], len(d.net.Pending()))
	return msg, nil
}

// BecomeIdle 把进程转为空闲。
func (d *Detector) BecomeIdle(p int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.endRound != 0 {
		return ErrTerminated
	}
	if outOfRange(d.n, p) {
		d.logf("input BecomeIdle(p=%d) output reject %v", p, ErrProcessOutOfRange)
		return ErrProcessOutOfRange
	}
	if d.states[p] == Idle {
		d.logf("input BecomeIdle(p=%d) output reject %v", p, ErrAlreadyIdle)
		return ErrAlreadyIdle
	}
	d.states[p] = Idle
	d.logf("input BecomeIdle(p=%d) output ok; state[%d]=Idle", p, p)
	return nil
}

// PassToken 由空闲的令牌持有者把令牌沿环传递。
// 非发起者传给 p-1；空闲发起者负责发起首轮/新一轮，并在令牌回到
// 0 时按“自身空闲且为白、令牌为白、累计值加自身计数为 0”判定终止。
// 返回 announced 为 true 时 round 为宣告轮数。
func (d *Detector) PassToken(p int) (announced bool, round int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.endRound != 0 {
		return false, d.endRound, ErrTerminated
	}
	if outOfRange(d.n, p) {
		d.logf("input PassToken(p=%d) output reject %v", p, ErrProcessOutOfRange)
		return false, 0, ErrProcessOutOfRange
	}
	if d.holder != p {
		d.logf("input PassToken(p=%d) output reject %v (holder=%d)", p, ErrNoToken, d.holder)
		return false, 0, ErrNoToken
	}
	if d.states[p] == Active {
		d.logf("input PassToken(p=%d) output reject %v", p, ErrActiveHolder)
		return false, 0, ErrActiveHolder
	}

	if p == 0 {
		return d.passAtInitiator()
	}

	// 非发起者：累计本地计数、按需染黑令牌、自身变白，再传给 p-1。
	d.accum += d.counts[p]
	if d.colors[p] == Black {
		d.token = Black
	}
	d.colors[p] = White
	d.holder = p - 1
	d.logf("input PassToken(p=%d) output accum=%d token=%s forwarded to %d",
		p, d.accum, colorName(d.token), d.holder)
	return false, d.round, nil

}

// passAtInitiator 处理空闲发起者持令牌时的全部逻辑；调用时已持锁。
func (d *Detector) passAtInitiator() (bool, int, error) {
	if !d.inFlight {
		d.round++
		d.token = White
		d.accum = 0
		d.colors[0] = White
		d.holder = (d.n - 1) % d.n // n==1 时仍为 0，令牌立刻“回到”发起者
		d.inFlight = true
		d.logf("input PassToken(p=0) output round %d started; token=White accum=0 forwarded to %d",
			d.round, d.holder)
		return false, d.round, nil
	}

	white := d.token == White && d.colors[0] == White
	total := d.accum + d.counts[0]
	d.logf("input PassToken(p=0) output token returned round=%d; judge: p0Idle=%v p0White=%v tokenWhite=%v accum=%d count[0]=%d sum=%d",
		d.round, d.states[0] == Idle, d.colors[0] == White, d.token == White, d.accum, d.counts[0], total)
	if white && total == 0 {
		d.inFlight = false
		d.endRound = d.round
		close(d.done)
		d.logf("decision TERMINATED at round %d: all counts sum to 0 and every visited process was White",
			d.round)
		return true, d.round, nil
	}

	d.round++
	d.token = White
	d.accum = 0
	d.colors[0] = White
	d.holder = (d.n - 1) % d.n
	d.logf("decision not terminated; round %d started; token=White accum=0 forwarded to %d",
		d.round, d.holder)
	return false, d.round, nil
}

// Snapshot 返回当前完整状态副本。
func (d *Detector) Snapshot() Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return Snapshot{
		States:  append([]State(nil), d.states...),
		Colors:  append([]Color(nil), d.colors...),
		Counts:  append([]int(nil), d.counts...),
		Holder:  d.holder,
		Token:   d.token,
		Accum:   d.accum,
		Round:   d.round,
		Pending: len(d.net.Pending()),
	}
}

// Announced 返回是否已宣告终止以及宣告时的轮数。
func (d *Detector) Announced() (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.endRound, d.endRound != 0
}

// Done 返回宣告终止时关闭的通道。
func (d *Detector) Done() <-chan struct{} { return d.done }

func outOfRange(n, p int) bool { return p < 0 || p >= n }

func (d *Detector) logf(format string, args ...any) {
	d.log.Logf(format, args...)
}

func colorName(c Color) string {
	if c == White {
		return "White"
	}
	return "Black"
}

type discardLogger struct{}

func (discardLogger) Logf(string, ...any) {}

// StringLogger 把日志保存在内存中，便于测试检查输入、输出与判定依据。
type StringLogger struct {
	mu  sync.Mutex
	buf []byte
}

func (l *StringLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, fmt.Sprintf(format+"\n", args...)...)
}

// String 返回截至目前的全部日志。
func (l *StringLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf)
}

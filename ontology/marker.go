// Package ontology 实现 RFC 4115 风格的双速率颜色感知三色标记器
// （带溢出耦合、红色惩罚期与在线改配置）。
package ontology

import (
	"errors"
	"fmt"
	"math/bits"
	"sync"
)

// Color 为报文颜色（输入或输出）。
type Color int

const (
	// Green 绿色。
	Green Color = iota
	// Yellow 黄色。
	Yellow
	// Red 红色。
	Red
	// Blind 色盲模式，判定时按 Green 处理。
	Blind
)

func (c Color) String() string {
	switch c {
	case Green:
		return "Green"
	case Yellow:
		return "Yellow"
	case Red:
		return "Red"
	case Blind:
		return "Blind"
	default:
		return "Unknown"
	}
}

// 可区分的错误原因。
var (
	ErrInvalidCIR        = errors.New("invalid CIR: must be in [1,1e6] bytes/ms")
	ErrInvalidCBS        = errors.New("invalid CBS: must be in [1,1e9]")
	ErrInvalidEBS        = errors.New("invalid EBS: must be in [0,1e9]")
	ErrInvalidWindow     = errors.New("invalid W: must be in [1,1e6] ms")
	ErrInvalidThreshold  = errors.New("invalid K: must be in [1,1000]")
	ErrInvalidPenaltyDur = errors.New("invalid Pn: must be in [1,1e6] ms")
	ErrInvalidColor      = errors.New("invalid color: must be Green/Yellow/Red/Blind")
	ErrInvalidBytes      = errors.New("invalid b: must be in [1,1e6]")
	ErrInvalidNow        = errors.New("invalid now: must be in [0,1e12]")
	ErrClockRollback     = errors.New("clock rollback: now is earlier than last settlement time")
)

// Result 为一次 Mark 的可观测结果。
type Result struct {
	// Color 为输出颜色。
	Color Color
	// Penalized 表示本次是否因处于惩罚期而直接判红。
	Penalized bool
	// Tc、Te 为结算并扣费后的两桶令牌量。
	Tc int64
	Te int64
	// InPenalty 表示返回时是否仍处于惩罚期（now < until）。
	InPenalty bool
	// Until 为当前惩罚截止时刻（0 表示从未触发过惩罚）。
	Until int64
	// RedQueueLen 为红记录队列当前长度。
	RedQueueLen int
	// Streak 为当前连击数 s。
	Streak int
	// Reason 为判定依据，供日志与精确复现使用。
	Reason string
}

// Config 为标记器参数。
type Config struct {
	CIR int64 // 承诺信息速率：每毫秒补充字节数
	CBS int64 // 承诺桶深
	EBS int64 // 超额桶深
	W   int64 // 红记录窗口（毫秒）
	K   int   // 红色阈值
	Pn  int64 // 基础惩罚时长（毫秒）
}

const (
	maxCIR = 1_000_000
	maxCBS = 1_000_000_000
	maxEBS = 1_000_000_000
	maxW   = 1_000_000
	maxK   = 1000
	maxPn  = 1_000_000
	maxNow = 1_000_000_000_000
	maxB   = 1_000_000
	maxS   = 3 // 连击封顶：dur = Pn * 2^s，最长 8*Pn
)

// Marker 为并发安全的双速率三色标记器。所有方法等价于按某个串行顺序执行。
type Marker struct {
	mu sync.Mutex

	cir int64
	cbs int64
	ebs int64
	w   int64
	k   int
	pn  int64

	// Tc 承诺桶，初值 CBS；Te 超额桶，初值 EBS；只接收 Tc 的溢出。
	tc int64
	te int64

	// last 上次结算时刻；until 惩罚截止时刻；lastUntil 上一次惩罚截止（0 表示无）。
	last      int64
	until     int64
	lastUntil int64

	// streak 连击数 s（0..3）。
	streak int

	// 红记录队列（单调递增）；head 为队首下标，摊还 O(1)。
	redQueue []int64
	redHead  int

	// 输出颜色统计。
	greenCount  int64
	greenBytes  int64
	yellowCount int64
	yellowBytes int64
	redCount    int64
	redBytes    int64

	// 历史上溢入 Te 的令牌总量（用于 Yellow 守恒界说明与测试）。
	totalOverflowToTe int64
}

// New 创建标记器：Tc=CBS、Te=EBS、last=until=lastUntil=0、s=0、红队列空。
func New(cfg Config) (*Marker, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	m := &Marker{
		cir: cfg.CIR, cbs: cfg.CBS, ebs: cfg.EBS, w: cfg.W, k: cfg.K, pn: cfg.Pn,
		tc:       cfg.CBS,
		te:       cfg.EBS,
		redQueue: make([]int64, 0, cfg.K),
	}
	return m, nil
}

func validateConfig(cfg Config) error {
	switch {
	case cfg.CIR < 1 || cfg.CIR > maxCIR:
		return ErrInvalidCIR
	case cfg.CBS < 1 || cfg.CBS > maxCBS:
		return ErrInvalidCBS
	case cfg.EBS < 0 || cfg.EBS > maxEBS:
		return ErrInvalidEBS
	case cfg.W < 1 || cfg.W > maxW:
		return ErrInvalidWindow
	case cfg.K < 1 || cfg.K > maxK:
		return ErrInvalidThreshold
	case cfg.Pn < 1 || cfg.Pn > maxPn:
		return ErrInvalidPenaltyDur
	}
	return nil
}

// mul64 返回 a*b，溢出（超过 int64）时返回 ok=false。
func mul64(a, b uint64) (int64, bool) {
	hi, lo := bits.Mul64(a, b)
	if hi != 0 || lo > 1<<63-1 {
		return 0, false
	}
	return int64(lo), true
}

// refillLocked 按批量公式 (now-last)*CIR 补充 Tc，溢出耦合到 Te。
// 调用方须持有锁并已完成参数校验与时钟回退检查。
func (m *Marker) refillLocked(now int64) {
	elapsed := uint64(now - m.last)
	add, _ := mul64(elapsed, uint64(m.cir)) // 合法 now/CIR 下必不溢出
	if add == 0 {
		m.last = now
		return
	}
	m.tc += add
	if m.tc > m.cbs {
		over := m.tc - m.cbs
		m.tc = m.cbs
		if over > m.ebs-m.te { // Te 已满：超出 EBS 的溢出部分丢弃
			over = m.ebs - m.te
		}
		m.te += over
		m.totalOverflowToTe += over
	}
	m.last = now
}

func (m *Marker) redLen() int { return len(m.redQueue) - m.redHead }

// pushRedLocked 先剔除 t+W<=now 的旧记录（恰等即剔除），再记录 now。
func (m *Marker) pushRedLocked(now int64) {
	cutoff := now - m.w // t+W <= now 等价于 t <= now-W
	for m.redHead < len(m.redQueue) && m.redQueue[m.redHead] <= cutoff {
		m.redHead++
	}
	// 队首垃圾过半（或全部过期）时压缩到新切片，保证每条记录的入队/剔除摊还 O(1)。
	if m.redHead > 0 && (m.redHead >= cap(m.redQueue)/2 || m.redHead >= len(m.redQueue)) {
		live := append([]int64(nil), m.redQueue[m.redHead:]...)
		m.redQueue = live
		m.redHead = 0
	}
	m.redQueue = append(m.redQueue, now)
}

func (m *Marker) account(c Color, b int64) {
	switch c {
	case Green:
		m.greenCount++
		m.greenBytes += b
	case Yellow:
		m.yellowCount++
		m.yellowBytes += b
	case Red:
		m.redCount++
		m.redBytes += b
	}
}

func validMarkArgs(now int64, color Color, b int64) error {
	// 参数非法优先于时钟回退。
	switch {
	case now < 0 || now > maxNow:
		return ErrInvalidNow
	case color < Green || color > Blind:
		return ErrInvalidColor
	case b < 1 || b > maxB:
		return ErrInvalidBytes
	}
	return nil
}

// Mark 按固定步骤标记报文：Refill -> 惩罚期判定 -> 输入色判定 ->
// 记红与惩罚触发 -> 输出统计。返回结果含判定依据 Reason，可精确复现。
func (m *Marker) Mark(now int64, color Color, b int64) (Result, error) {
	if err := validMarkArgs(now, color, b); err != nil {
		return Result{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.last {
		return Result{}, ErrClockRollback
	}

	m.refillLocked(now)

	out := Red
	penalized := false
	reason := ""

	if now < m.until {
		// 惩罚期内：任何输入色一律 Red，不扣令牌、不记红。
		out = Red
		penalized = true
		reason = fmt.Sprintf("penalty active: now=%d < until=%d, no tokens deducted, no red recorded", now, m.until)
	} else {
		switch color {
		case Green, Blind:
			switch {
			case m.tc >= b:
				m.tc -= b
				out = Green
				reason = fmt.Sprintf("input=%s: Tc=%d >= b=%d, deduct from Tc", color, m.tc+b, b)
			case m.te >= b:
				m.te -= b
				out = Yellow
				reason = fmt.Sprintf("input=%s: Tc=%d < b=%d and Te=%d >= b, downgrade, deduct from Te", color, m.tc, b, m.te+b)
			default:
				out = Red
				reason = fmt.Sprintf("input=%s: Tc=%d < b=%d and Te=%d < b=%d, red", color, m.tc, b, m.te, b)
			}
		case Yellow:
			// Yellow 输入只用超额桶，绝不触碰 Tc。
			if m.te >= b {
				m.te -= b
				out = Yellow
				reason = fmt.Sprintf("input=Yellow: Te=%d >= b=%d, deduct from Te, Tc untouched", m.te+b, b)
			} else {
				out = Red
				reason = fmt.Sprintf("input=Yellow: Te=%d < b=%d, red, Tc untouched", m.te, b)
			}
		case Red:
			// Red 输入直接红，不扣令牌、不记红。
			out = Red
			reason = "input=Red: red without deducting tokens, not recorded"
		}

		// 记红：输入非 Red、输出 Red、且不在惩罚期。
		if color != Red && out == Red {
			m.pushRedLocked(now)
			reason += fmt.Sprintf("; red recorded at now=%d, queue len=%d", now, m.redLen())
			if m.redLen() >= m.k {
				m.triggerPenaltyLocked(now)
				reason += fmt.Sprintf("; threshold K=%d reached, streak s=%d, dur=%d, until=%d",
					m.k, m.streak, m.until-now, m.until)
				m.redQueue = m.redQueue[:0]
				m.redHead = 0
			}
		}
	}

	m.account(out, b) // 输入字节恰计入某个输出色

	return Result{
		Color:       out,
		Penalized:   penalized,
		Tc:          m.tc,
		Te:          m.te,
		InPenalty:   now < m.until,
		Until:       m.until,
		RedQueueLen: m.redLen(),
		Streak:      m.streak,
		Reason:      reason,
	}, nil
}

// triggerPenaltyLocked 在红队列达到 K 时调用：连击判定与惩罚时长递增。
// 上一次惩罚结束后未满一个窗口（now-lastUntil < W，恰等不算）则连击，
// 否则连击清零；dur=Pn*2^s（s 封顶 3，最长 8*Pn）。
func (m *Marker) triggerPenaltyLocked(now int64) {
	if m.lastUntil > 0 && now-m.lastUntil < m.w {
		if m.streak < maxS {
			m.streak++
		}
	} else {
		m.streak = 0
	}
	dur := m.pn << m.streak
	m.until = now + dur
	m.lastUntil = m.until
}

// Reconfigure 先按旧参数在 now 结算，再截断 Tc/Te（截去部分丢弃，
// 不转入超额桶），最后切换速率/桶深；惩罚状态与红队列保持不变。
func (m *Marker) Reconfigure(now, cir, cbs, ebs int64) error {
	if err := validateConfig(Config{CIR: cir, CBS: cbs, EBS: ebs, W: 1, K: 1, Pn: 1}); err != nil {
		return err
	}
	if now < 0 || now > maxNow {
		return ErrInvalidNow
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.last {
		return ErrClockRollback
	}

	m.refillLocked(now)
	if m.tc > cbs {
		m.tc = cbs
	}
	if m.te > ebs {
		m.te = ebs
	}
	m.cir, m.cbs, m.ebs = cir, cbs, ebs
	return nil
}

// Snapshot 返回当前可观测状态的快照。
type Snapshot struct {
	Now         int64
	Tc          int64
	Te          int64
	Last        int64
	Until       int64
	LastUntil   int64
	Streak      int
	RedQueue    []int64
	GreenCount  int64
	GreenBytes  int64
	YellowCount int64
	YellowBytes int64
	RedCount    int64
	RedBytes    int64
	CIR         int64
	CBS         int64
	EBS         int64
}

// State 返回在 now 处先 Refill 后的完整状态快照（不记录任何报文）。
func (m *Marker) State(now int64) (Snapshot, error) {
	if now < 0 || now > maxNow {
		return Snapshot{}, ErrInvalidNow
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.last {
		return Snapshot{}, ErrClockRollback
	}
	m.refillLocked(now)

	q := make([]int64, m.redLen())
	copy(q, m.redQueue[m.redHead:])

	return Snapshot{
		Now:         now,
		Tc:          m.tc,
		Te:          m.te,
		Last:        m.last,
		Until:       m.until,
		LastUntil:   m.lastUntil,
		Streak:      m.streak,
		RedQueue:    q,
		GreenCount:  m.greenCount,
		GreenBytes:  m.greenBytes,
		YellowCount: m.yellowCount,
		YellowBytes: m.yellowBytes,
		RedCount:    m.redCount,
		RedBytes:    m.redBytes,
		CIR:         m.cir,
		CBS:         m.cbs,
		EBS:         m.ebs,
	}, nil
}

// Counters 返回各输出颜色的累计报文数与字节数。
func (m *Marker) Counters() (greenCount, greenBytes, yellowCount, yellowBytes, redCount, redBytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.greenCount, m.greenBytes, m.yellowCount, m.yellowBytes, m.redCount, m.redBytes
}

// TotalOverflowToTe 返回历史上由 Tc 溢入 Te 的令牌总量。
func (m *Marker) TotalOverflowToTe() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalOverflowToTe
}

package creditflow

import (
	"log"
	"os"
	"sync"
)

// Message 是一条被编号的消息。
type Message struct {
	Seq     int
	Payload []byte
}

// Stats 是某一时刻通道的完整状态快照。
type Stats struct {
	Credit   int // 发送端可用信用
	Backlog  int // 积压条数（已产生未发送）
	InFlight int // 在途消息（已发送未收到）
	Buffered int // 接收缓冲占用（已收到未消费）
	Produced int // 已产生总数
	Received int // 已收到总数
	Consumed int // 已消费总数
}

// Channel 是一个发送端与一个接收端之间的基于信用的点对点流控通道。
// 所有状态由一把互斥锁保护，发送侧与接收侧操作可被不同执行体并发调用。
type Channel struct {
	mu sync.Mutex

	capacity   int
	maxBacklog int
	logger     Logger

	credit  int
	backlog []Message

	inFlight []Message // 已发送、尚未被接收端取走
	buffered []Message // 接收端已取走、尚未消费，按编号顺序

	produced int
	received int
	consumed int

	nextSeq int // 下一条待产生消息的编号（从 1 起）
	wantSeq int // 下一条应被消费的编号（从 1 起）
	maxSeen int // 已到达接收侧的最大编号，用于消费越界判断

	sender   *Sender
	receiver *Receiver
}

// NewChannel 创建通道。capacity 为接收缓冲容量，maxBacklog 为积压上限，
// 二者必须为正。logger 为 nil 时使用写到 stderr 的默认日志器。
func NewChannel(capacity, maxBacklog int, logger Logger) (*Channel, error) {
	if capacity <= 0 || maxBacklog <= 0 {
		return nil, ErrInvalidParam
	}
	if logger == nil {
		logger = log.New(os.Stderr, "creditflow: ", log.LstdFlags|log.Lmicroseconds)
	}
	c := &Channel{
		capacity:   capacity,
		maxBacklog: maxBacklog,
		logger:     logger,
		nextSeq:    1,
		wantSeq:    1,
	}
	c.sender = &Sender{c: c}
	c.receiver = &Receiver{c: c}
	c.logf("init capacity=%d maxBacklog=%d | 判定: 通道建立，初始信用/积压/缓冲均为0", capacity, maxBacklog)
	return c, nil
}

// Sender 返回发送端视图。
func (c *Channel) Sender() *Sender { return c.sender }

// Receiver 返回接收端视图。
func (c *Channel) Receiver() *Receiver { return c.receiver }

// Stats 返回当前状态快照（线程安全）。
func (c *Channel) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot()
}

func (c *Channel) snapshot() Stats {
	return Stats{
		Credit:   c.credit,
		Backlog:  len(c.backlog),
		InFlight: len(c.inFlight),
		Buffered: len(c.buffered),
		Produced: c.produced,
		Received: c.received,
		Consumed: c.consumed,
	}
}

func (c *Channel) logf(format string, args ...any) {
	if c.logger != nil {
		c.logger.Printf(format, args...)
	}
}

// pump 在锁内尽可能多地把积压消息自动发出：先扣信用再发送，
// 信用为零时一条不发。
func (c *Channel) pump(reason string) int {
	sent := 0
	for c.credit > 0 && len(c.backlog) > 0 {
		msg := c.backlog[0]
		c.backlog = c.backlog[1:]
		c.credit-- // 先扣信用
		c.inFlight = append(c.inFlight, msg)
		sent++
		s := c.snapshot()
		c.logf("pump seq=%d reason=%q | credit=%d backlog=%d inflight=%d buffered=%d | 判定: 有信用且有积压，先扣信用后发送",
			msg.Seq, reason, s.Credit, s.Backlog, s.InFlight, s.Buffered)
	}
	if sent == 0 {
		s := c.snapshot()
		c.logf("pump reason=%q | credit=%d backlog=%d inflight=%d buffered=%d | 判定: 信用为0或积压为空，不发送",
			reason, s.Credit, s.Backlog, s.InFlight, s.Buffered)
	}
	return sent
}

// grant 按“缓冲余量 - 在途信用”计算可授予额度；为正则授予对应信用，
// 否则什么都不做。在锁内调用。
func (c *Channel) grant(why string) int {
	reserved := c.credit + len(c.inFlight) // 在途信用：已授予但尚未对应到接收缓冲的信用
	free := c.capacity - len(c.buffered) - reserved
	s := c.snapshot()
	if free <= 0 {
		c.logf("grant why=%q free=(capacity-buffered-inflightcredit)=%d | credit=%d backlog=%d inflight=%d buffered=%d | 判定: 余量非正，不授予",
			why, free, s.Credit, s.Backlog, s.InFlight, s.Buffered)
		return 0
	}
	c.credit += free
	s = c.snapshot()
	c.logf("grant why=%q free=%d | credit=%d backlog=%d inflight=%d buffered=%d | 判定: 余量为正，授予%d信用",
		why, free, s.Credit, s.Backlog, s.InFlight, s.Buffered, free)
	return free
}

// Sender 发送端：产生消息、查状态、探测信用。
type Sender struct{ c *Channel }

// Produce 产生一条消息：消息先进入积压，再自动发送。
// 若积压已满，整体拒绝（ErrBacklogOverflow）且不改变任何状态。
func (s *Sender) Produce(payload []byte) (int, error) {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()

	before := c.snapshot()
	if len(c.backlog) >= c.maxBacklog {
		c.logf("produce REJECT backlog=%d maxBacklog=%d | credit=%d inflight=%d buffered=%d | 原因: ErrBacklogOverflow，失败不留痕",
			before.Backlog, c.maxBacklog, before.Credit, before.InFlight, before.Buffered)
		return 0, ErrBacklogOverflow
	}

	msg := Message{Seq: c.nextSeq, Payload: append([]byte(nil), payload...)}
	c.nextSeq++
	c.produced++
	c.backlog = append(c.backlog, msg)
	st := c.snapshot()
	c.logf("produce seq=%d bytes=%d | credit=%d backlog=%d inflight=%d buffered=%d | 判定: 先入积压再自动发送",
		msg.Seq, len(payload), st.Credit, st.Backlog, st.InFlight, st.Buffered)

	c.pump("produce")
	return msg.Seq, nil
}

// Credit 返回发送端当前可用信用。
func (s *Sender) Credit() int {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	return s.c.credit
}

// Backlog 返回当前积压条数。
func (s *Sender) Backlog() int {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	return len(s.c.backlog)
}

// Probe 仅在无信用且有积压时允许；效果与一次 Advertise 相同（不保底、不记忆）。
// 条件不满足时返回 ErrProbeNotAllowed 且不改变任何状态。
func (s *Sender) Probe() (int, error) {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()

	before := c.snapshot()
	if c.credit != 0 || len(c.backlog) == 0 {
		c.logf("probe REJECT credit=%d backlog=%d inflight=%d buffered=%d | 原因: ErrProbeNotAllowed（需无信用且有积压），失败不留痕",
			before.Credit, before.Backlog, before.InFlight, before.Buffered)
		return 0, ErrProbeNotAllowed
	}
	granted := c.grant("probe")
	c.pump("probe")
	return granted, nil
}

// Receiver 接收端：取消息、顺序消费、主动通告、查状态。
type Receiver struct{ c *Channel }

// Deliver 把在途消息按编号顺序取入接收缓冲，并返回本次取入的消息副本。
// 该操作不触发通告。
func (r *Receiver) Deliver() []Message {
	c := r.c
	c.mu.Lock()
	defer c.mu.Unlock()

	taken := make([]Message, 0, len(c.inFlight))
	for _, msg := range c.inFlight {
		taken = append(taken, Message{Seq: msg.Seq, Payload: append([]byte(nil), msg.Payload...)})
		c.buffered = append(c.buffered, msg)
		c.received++
		if msg.Seq > c.maxSeen {
			c.maxSeen = msg.Seq
		}
	}
	c.inFlight = c.inFlight[:0]
	s := c.snapshot()
	c.logf("deliver count=%d | credit=%d backlog=%d inflight=%d buffered=%d | 判定: 在途消息进入接收缓冲，不触发通告",
		len(taken), s.Credit, s.Backlog, s.InFlight, s.Buffered)
	return taken
}

// Consume 按编号顺序消费一条已收到的消息：seq 必须等于下一期望编号，
// 且该消息确已在接收缓冲中。消费不触发通告。
func (r *Receiver) Consume(seq int) error {
	c := r.c
	c.mu.Lock()
	defer c.mu.Unlock()

	before := c.snapshot()
	switch {
	case seq <= 0:
		c.logf("consume REJECT seq=%d | credit=%d backlog=%d inflight=%d buffered=%d | 原因: ErrInvalidParam（编号非正），失败不留痕",
			seq, before.Credit, before.Backlog, before.InFlight, before.Buffered)
		return ErrInvalidParam
	case seq < c.wantSeq:
		c.logf("consume REJECT seq=%d want=%d | credit=%d backlog=%d inflight=%d buffered=%d | 原因: ErrInvalidParam（编号重复/过期），失败不留痕",
			seq, c.wantSeq, before.Credit, before.Backlog, before.InFlight, before.Buffered)
		return ErrInvalidParam
	case seq > c.wantSeq, c.wantSeq > c.maxSeen:
		c.logf("consume REJECT seq=%d want=%d maxSeen=%d | credit=%d backlog=%d inflight=%d buffered=%d | 原因: ErrInvalidParam（越界或跳号），失败不留痕",
			seq, c.wantSeq, c.maxSeen, before.Credit, before.Backlog, before.InFlight, before.Buffered)
		return ErrInvalidParam
	}

	// wantSeq 对应的消息一定位于缓冲头部（编号顺序保证）。
	c.buffered = c.buffered[1:]
	c.consumed++
	c.wantSeq++
	s := c.snapshot()
	c.logf("consume seq=%d | credit=%d backlog=%d inflight=%d buffered=%d | 判定: 顺序消费，不触发通告",
		seq, s.Credit, s.Backlog, s.InFlight, s.Buffered)
	return nil
}

// Advertise 按缓冲余量与在途信用之差授予信用；为正则授予对应额度，
// 否则什么都不做。授予后发送端积压消息自动发出。
func (r *Receiver) Advertise() int {
	c := r.c
	c.mu.Lock()
	defer c.mu.Unlock()

	granted := c.grant("advertise")
	c.pump("advertise")
	return granted
}

// Buffer 返回接收缓冲中已占用的条数。
func (r *Receiver) Buffer() int {
	c := r.c
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.buffered)
}

// InFlight 返回在途（已发送未收到）消息条数。
func (r *Receiver) InFlight() int {
	c := r.c
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inFlight)
}

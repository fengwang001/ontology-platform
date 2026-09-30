// Package neighbor 实现邻居缓存状态机（RFC 4861 邻居不可达检测的简化模型）。
package neighbor

import (
	"sort"
	"sync"
	"time"
)

// State 为邻居条目状态。
type State int

const (
	Incomplete State = iota // 未完成：地址解析进行中
	Reachable               // 可达
	Stale                   // 陈旧
	Delay                   // 延迟
	Probe                   // 探测
)

func (s State) String() string { return stateNames[s] }

var stateNames = [...]string{"INCOMPLETE", "REACHABLE", "STALE", "DELAY", "PROBE"}

// Config 为状态机参数。
type Config struct {
	ReachableTime time.Duration // R：可达到期时间
	DelayTime     time.Duration // Dl：延迟到期时间
	RetransTimer  time.Duration // T：重发间隔
	MaxAttempts   int           // K：请求最多发送次数
	QueueLimit    int           // Q：每条目暂存包上限
	MaxEntries    int           // 条目数上限
}

// EntryView 为条目只读快照。
type EntryView struct {
	Address   string
	LinkLayer string
	State     State
	Deadline  time.Time
	Sent      int
	Queued    int
}

// Cache 为并发安全的邻居缓存。
type Cache struct {
	cfg Config
	mu  sync.Mutex

	entries  map[string]*entry
	lastTime time.Time
	lastOK   bool

	log             Logger
	output          func(packet any, linkLayer string)
	emitRequestFunc func(address string)

	unreachableDrops int
	overflowDrops    int
}

// NewCache 创建邻居缓存。
// output 用于放出数据包（参数为包与解析到的链路层地址）；
// emitRequest 用于发出地址解析请求，可为 nil（此时不回调但仍计数发送次数）；
// log 可为 nil，nil 时不记录日志。
func NewCache(cfg Config, output func(packet any, linkLayer string), emitRequest func(address string), log Logger) *Cache {
	if log == nil {
		log = nopLogger{}
	}
	if output == nil {
		output = func(any, string) {}
	}
	if emitRequest == nil {
		emitRequest = func(string) {}
	}
	return &Cache{
		cfg:             cfg,
		entries:         make(map[string]*entry),
		log:             log,
		output:          output,
		emitRequestFunc: emitRequest,
	}
}

// deferred 收集锁内产生的回调，解锁后再执行，避免回调重入造成死锁。
type deferred struct {
	requests []string
	delivers []delivery
}

type delivery struct {
	packet    any
	linkLayer string
}

func (d *deferred) request(address string) { d.requests = append(d.requests, address) }

func (d *deferred) deliver(packets []any, linkLayer string) {
	for _, p := range packets {
		d.delivers = append(d.delivers, delivery{packet: p, linkLayer: linkLayer})
	}
}

func (d *deferred) deliverOne(packet any, linkLayer string) {
	d.delivers = append(d.delivers, delivery{packet: packet, linkLayer: linkLayer})
}

func (c *Cache) run(def *deferred) {
	for _, address := range def.requests {
		c.emitRequestFunc(address)
	}
	for _, dv := range def.delivers {
		c.output(dv.packet, dv.linkLayer)
	}
}

// expireAll 对每个条目至多处理一个到期动作；按地址排序保证重放确定。
func (c *Cache) expireAll(now time.Time, def *deferred) {
	addresses := make([]string, 0, len(c.entries))
	for address := range c.entries {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		e := c.entries[address]
		deleted, dropped := e.expire(now, c.cfg, def.request)
		if deleted {
			delete(c.entries, address)
			c.unreachableDrops += dropped
			c.log.Logf("expire t=%d addr=%s INCOMPLETE/PROBE attempts=%d>=K -> delete, %d queued packet(s) dropped as unreachable",
				now.UnixNano(), address, e.sent, dropped)
			continue
		}
		switch e.state {
		case Stale:
			c.log.Logf("expire t=%d addr=%s REACHABLE -> STALE", now.UnixNano(), address)
		case Probe:
			c.log.Logf("expire t=%d addr=%s DELAY -> PROBE, send probe #1, next retry t=%d",
				now.UnixNano(), address, e.deadline.UnixNano())
		}
	}
}

func (c *Cache) checkClock(now time.Time) error {
	if c.lastOK && now.Before(c.lastTime) {
		c.log.Logf("reject t=%d: clock moved backwards (last t=%d)", now.UnixNano(), c.lastTime.UnixNano())
		return ErrClockBackward
	}
	return nil
}

func (c *Cache) commitTime(now time.Time) {
	c.lastTime = now
	c.lastOK = true
}

// Tick 只推进时刻并做到期处理。
func (c *Cache) Tick(now time.Time) error {
	c.mu.Lock()
	def := &deferred{}
	if err := c.checkClock(now); err != nil {
		c.mu.Unlock()
		return err
	}
	c.expireAll(now, def)
	c.commitTime(now)
	c.log.Logf("tick t=%d: %d entries, %d request(s), %d packet(s) out",
		now.UnixNano(), len(c.entries), len(def.requests), len(def.delivers))
	c.mu.Unlock()
	c.run(def)
	return nil
}

// Send 发送一个目的为 address 的包。
func (c *Cache) Send(now time.Time, address string, packet any) error {
	c.mu.Lock()
	def := &deferred{}
	// 任何操作先做到期处理；拒绝发生在到期处理之后。
	if err := c.checkClock(now); err != nil {
		c.mu.Unlock()
		return err
	}
	c.expireAll(now, def)

	if address == "" {
		c.mu.Unlock()
		c.log.Logf("reject send t=%d: empty address", now.UnixNano())
		c.run(def)
		return ErrEmptyAddress
	}
	e, ok := c.entries[address]
	if !ok && c.cfg.MaxEntries > 0 && len(c.entries) >= c.cfg.MaxEntries {
		c.mu.Unlock()
		c.log.Logf("reject send t=%d addr=%s: entry limit %d reached", now.UnixNano(), address, c.cfg.MaxEntries)
		c.run(def)
		return ErrEntryLimit
	}

	if !ok {
		// 无条目：建未完成条目、暂存该包、发第 1 次请求。
		e = &entry{address: address, state: Incomplete}
		c.entries[address] = e
		c.overflowDrops += e.enqueue(packet, c.cfg)
		e.sent = 1
		e.deadline = now.Add(c.cfg.RetransTimer)
		def.request(address)
		c.commitTime(now)
		c.log.Logf("send t=%d addr=%s: no entry -> INCOMPLETE, queue=1, send request #1, next retry t=%d",
			now.UnixNano(), address, e.deadline.UnixNano())
		c.mu.Unlock()
		c.run(def)
		return nil
	}

	switch e.state {
	case Incomplete:
		before := len(e.queue)
		overflow := e.enqueue(packet, c.cfg)
		c.overflowDrops += overflow
		c.commitTime(now)
		c.log.Logf("send t=%d addr=%s: INCOMPLETE -> queue (before=%d after=%d, overflow dropped=%d)",
			now.UnixNano(), address, before, len(e.queue), overflow)
	case Reachable, Delay, Probe:
		def.deliverOne(packet, e.linkLayer)
		c.commitTime(now)
		c.log.Logf("send t=%d addr=%s: state=%s -> deliver packet directly via %s",
			now.UnixNano(), address, e.state, e.linkLayer)
	case Stale:
		def.deliverOne(packet, e.linkLayer)
		e.state = Delay
		e.deadline = now.Add(c.cfg.DelayTime)
		c.commitTime(now)
		c.log.Logf("send t=%d addr=%s: STALE -> deliver via %s and DELAY, expire t=%d",
			now.UnixNano(), address, e.linkLayer, e.deadline.UnixNano())
	}
	c.mu.Unlock()
	c.run(def)
	return nil
}

// Advertise 处理来自对端的应答。
// linkLayer 为应答携带的链路层地址；solicited 表示是否为对本方请求的回应；
// override 表示应答是否要求覆盖已有链路层地址。
func (c *Cache) Advertise(now time.Time, address, linkLayer string, solicited, override bool) error {
	c.mu.Lock()
	def := &deferred{}
	if err := c.checkClock(now); err != nil {
		c.mu.Unlock()
		return err
	}
	c.expireAll(now, def)

	if address == "" {
		c.mu.Unlock()
		c.log.Logf("reject advertise t=%d: empty address", now.UnixNano())
		c.run(def)
		return ErrEmptyAddress
	}
	if linkLayer == "" {
		c.mu.Unlock()
		c.log.Logf("reject advertise t=%d addr=%s: empty link-layer address", now.UnixNano(), address)
		c.run(def)
		return ErrEmptyLinkLayer
	}
	e, ok := c.entries[address]
	if !ok {
		c.mu.Unlock()
		c.log.Logf("reject advertise t=%d addr=%s: entry not found", now.UnixNano(), address)
		c.run(def)
		return ErrNoEntry
	}

	addrChanged := e.linkLayer != linkLayer
	switch {
	case e.state == Incomplete:
		e.linkLayer = linkLayer
		queued := e.drainQueue()
		def.deliver(queued, linkLayer)
		if solicited {
			e.state = Reachable
			e.deadline = now.Add(c.cfg.ReachableTime)
		} else {
			e.state = Stale
			e.deadline = time.Time{}
		}
		c.log.Logf("advertise t=%d addr=%s ll=%s solicited=%v: INCOMPLETE -> %s, record ll, release %d queued packet(s)",
			now.UnixNano(), address, linkLayer, solicited, e.state, len(queued))
	case override || !addrChanged:
		e.linkLayer = linkLayer
		switch {
		case solicited:
			e.state = Reachable
			e.deadline = now.Add(c.cfg.ReachableTime)
			c.log.Logf("advertise t=%d addr=%s ll=%s override=%v: response -> REACHABLE, record ll",
				now.UnixNano(), address, linkLayer, override)
		case addrChanged:
			e.state = Stale
			e.deadline = time.Time{}
			c.log.Logf("advertise t=%d addr=%s ll=%s override=%v: unsolicited with changed ll -> STALE, record ll",
				now.UnixNano(), address, linkLayer, override)
		default:
			c.log.Logf("advertise t=%d addr=%s ll=%s override=%v: unsolicited same ll -> state stays %s",
				now.UnixNano(), address, linkLayer, override, e.state)
		}
	default:
		// 不覆盖且地址不同。
		if e.state == Reachable {
			e.state = Stale
			e.deadline = time.Time{}
			c.log.Logf("advertise t=%d addr=%s ll=%s: no-override different ll, REACHABLE -> STALE, ignore ll",
				now.UnixNano(), address, linkLayer)
		} else {
			c.log.Logf("advertise t=%d addr=%s ll=%s: no-override different ll, state %s ignores without recording",
				now.UnixNano(), address, linkLayer, e.state)
		}
	}
	c.commitTime(now)
	c.mu.Unlock()
	c.run(def)
	return nil
}

// Confirm 处理上层可达性确认：仅使延迟或探测转可达。
func (c *Cache) Confirm(now time.Time, address string) error {
	c.mu.Lock()
	def := &deferred{}
	if err := c.checkClock(now); err != nil {
		c.mu.Unlock()
		return err
	}
	c.expireAll(now, def)

	if address == "" {
		c.mu.Unlock()
		c.log.Logf("reject confirm t=%d: empty address", now.UnixNano())
		c.run(def)
		return ErrEmptyAddress
	}
	e, ok := c.entries[address]
	if !ok {
		c.mu.Unlock()
		c.log.Logf("reject confirm t=%d addr=%s: entry not found", now.UnixNano(), address)
		c.run(def)
		return ErrNoEntry
	}
	if e.state == Delay || e.state == Probe {
		c.log.Logf("confirm t=%d addr=%s: %s -> REACHABLE", now.UnixNano(), address, e.state)
		e.state = Reachable
		e.deadline = now.Add(c.cfg.ReachableTime)
	} else {
		c.log.Logf("confirm t=%d addr=%s: state %s unaffected", now.UnixNano(), address, e.state)
	}
	c.commitTime(now)
	c.mu.Unlock()
	c.run(def)
	return nil
}

// Snapshot 返回按地址排序的条目快照。
func (c *Cache) Snapshot() []EntryView {
	c.mu.Lock()
	defer c.mu.Unlock()
	addresses := make([]string, 0, len(c.entries))
	for address := range c.entries {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	views := make([]EntryView, 0, len(addresses))
	for _, address := range addresses {
		e := c.entries[address]
		views = append(views, EntryView{
			Address:   address,
			LinkLayer: e.linkLayer,
			State:     e.state,
			Deadline:  e.deadline,
			Sent:      e.sent,
			Queued:    len(e.queue),
		})
	}
	return views
}

// Stats 返回不可达丢弃与暂存溢出丢弃的包数。
func (c *Cache) Stats() (unreachable, overflow int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unreachableDrops, c.overflowDrops
}

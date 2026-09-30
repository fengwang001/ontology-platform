// Package conntrack 实现一个源地址转换（SNAT）连接跟踪表。
package conntrack

import (
	"errors"
	"fmt"
	"sync"
)

// ConnState 表示连接跟踪条目当前所处的状态。
type ConnState int

const (
	// StateHalfOpen 半开：已见到出站 SYN，尚未完成握手。
	StateHalfOpen ConnState = iota
	// StateEstablished 已建立：见到了入站 SYN 或 DATA。
	StateEstablished
	// StateClosed 关闭：见到 FIN，等待超时后回收。
	StateClosed
)

func (s ConnState) String() string {
	switch s {
	case StateHalfOpen:
		return "HALF_OPEN"
	case StateEstablished:
		return "ESTABLISHED"
	case StateClosed:
		return "CLOSED"
	default:
		return "UNKNOWN"
	}
}

// PacketKind 是事件携带的报文种类。
type PacketKind int

const (
	// PktSYN 同步报文。
	PktSYN PacketKind = iota
	// PktDATA 数据报文。
	PktDATA
	// PktFIN 正常关闭报文。
	PktFIN
	// PktRST 复位报文。
	PktRST
)

func (k PacketKind) String() string {
	switch k {
	case PktSYN:
		return "SYN"
	case PktDATA:
		return "DATA"
	case PktFIN:
		return "FIN"
	case PktRST:
		return "RST"
	default:
		return "UNKNOWN"
	}
}

// Endpoint 标识一个 IP 端点（地址 + 端口）。
type Endpoint struct {
	Addr string
	Port uint16
}

// ConnKey 是一条连接的四元组标识：内部端点 + 远端端点。
type ConnKey struct {
	Src Endpoint
	Dst Endpoint
}

// ConnInfo 是查询返回的连接快照。
type ConnInfo struct {
	Key      ConnKey
	ExtPort  uint16
	State    ConnState
	ExpireAt int64
}

// 拒绝原因。按约定，每次操作至多返回其中一个错误。
var (
	// ErrClockRewind 事件时刻早于本会话已见到的最大时刻（时钟回拨）。
	ErrClockRewind = errors.New("conntrack: clock moved backwards")
	// ErrNotSYN 入站/出站报文不属于任何连接，且又不是用于新建连接的出站 SYN。
	ErrNotSYN = errors.New("conntrack: no matching connection and packet is not SYN")
	// ErrTableFull 需要新建连接，但活动连接数已达上限 N。
	ErrTableFull = errors.New("conntrack: connection table full")
	// ErrPortPoolExhausted 需要为新内部端点分配外部端口，但端口池已无空闲端口。
	ErrPortPoolExhausted = errors.New("conntrack: external port pool exhausted")
	// ErrNoMapping 入站报文的外部端口在表上没有任何内部端点映射。
	ErrNoMapping = errors.New("conntrack: no mapping for external port")
	// ErrNoConnection 外部端口有映射，但不存在来自该远端端点的连接。
	ErrNoConnection = errors.New("conntrack: no connection for that remote endpoint")
)

// Logger 用于记录事件输入、输出与判定依据。传 nil 将不输出日志。
type Logger interface {
	Printf(format string, args ...any)
}

// Config 是连接跟踪表的构造参数。
type Config struct {
	// Lo、Hi 为外部端口闭区间 [Lo, Hi]。
	Lo uint16
	Hi uint16
	// N 为活动连接数上限。
	N int
	// HalfOpenTimeout、EstablishedTimeout、ClosedTimeout 分别为
	// 半开、已建立、关闭三种状态的超时（必须为正）。
	HalfOpenTimeout    int64
	EstablishedTimeout int64
	ClosedTimeout      int64
	Logger             Logger
}

// conn 是单条连接的内部记录。
type conn struct {
	key      ConnKey
	extPort  uint16
	state    ConnState
	expireAt int64
}

// Table 是并发安全的 SNAT 连接跟踪表。
type Table struct {
	mu sync.Mutex

	lo uint16
	hi uint16
	n  int
	th int64
	te int64
	tc int64

	logger Logger

	lastTime int64
	timeSet  bool

	conns map[ConnKey]*conn
	// extOwner[外部端口] = 当前占用该端口的内部端点。
	extOwner map[uint16]Endpoint
	// ownerConns[内部端点] = 该端点仍存活的连接键集合（引用计数）。
	ownerConns map[Endpoint]map[ConnKey]struct{}
	// 用于反查入站连接：extPort -> 远端端点 -> 连接键。
	extIndex map[uint16]map[Endpoint]ConnKey
}

// New 创建连接跟踪表。参数非法时返回错误且不产生任何副作用。
func New(cfg Config) (*Table, error) {
	if cfg.Lo > cfg.Hi {
		return nil, fmt.Errorf("conntrack: invalid port range [%d,%d]: lo > hi", cfg.Lo, cfg.Hi)
	}
	if cfg.N <= 0 {
		return nil, fmt.Errorf("conntrack: connection limit N must be positive, got %d", cfg.N)
	}
	if cfg.HalfOpenTimeout <= 0 {
		return nil, fmt.Errorf("conntrack: half-open timeout must be positive, got %d", cfg.HalfOpenTimeout)
	}
	if cfg.EstablishedTimeout <= 0 {
		return nil, fmt.Errorf("conntrack: established timeout must be positive, got %d", cfg.EstablishedTimeout)
	}
	if cfg.ClosedTimeout <= 0 {
		return nil, fmt.Errorf("conntrack: closed timeout must be positive, got %d", cfg.ClosedTimeout)
	}
	return &Table{
		lo:         cfg.Lo,
		hi:         cfg.Hi,
		n:          cfg.N,
		th:         cfg.HalfOpenTimeout,
		te:         cfg.EstablishedTimeout,
		tc:         cfg.ClosedTimeout,
		logger:     cfg.Logger,
		conns:      make(map[ConnKey]*conn),
		extOwner:   make(map[uint16]Endpoint),
		ownerConns: make(map[Endpoint]map[ConnKey]struct{}),
		extIndex:   make(map[uint16]map[Endpoint]ConnKey),
	}, nil
}

// Outcome 描述一次出站/入站处理的结果。
type Outcome struct {
	Allowed  bool
	ExtPort  uint16
	State    ConnState
	ExpireAt int64
	Created  bool
	Deleted  bool
}

// ProcessOutbound 处理一个出站事件。
func (t *Table) ProcessOutbound(now int64, key ConnKey, kind PacketKind) (Outcome, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	src, dst := key.Src, key.Dst
	t.logf("OUT  now=%d src=%s:%d dst=%s:%d kind=%s", now, src.Addr, src.Port, dst.Addr, dst.Port, kind)

	if t.timeSet && now < t.lastTime {
		t.logf("OUT  -> REJECT clock-rewind: now=%d < last=%d", now, t.lastTime)
		return Outcome{}, ErrClockRewind
	}

	c, exists := t.conns[key]
	live := exists && c.expireAt > now
	if !live && kind != PktSYN {
		t.logf("OUT  -> REJECT not-syn: no live connection for 4-tuple and kind=%s", kind)
		return Outcome{}, ErrNotSYN
	}

	// 此后操作必被接受：先回收所有已到期条目（到期即不存在，
	// 其连接数与端口不再占用资源），再执行状态迁移。
	t.sweepLocked(now)

	if !live {
		// 仅出站 SYN 可新建连接。
		if len(t.conns) >= t.n {
			t.logf("OUT  -> REJECT table-full: active=%d >= N=%d", len(t.conns), t.n)
			return Outcome{}, ErrTableFull
		}
		extPort, ok := t.portForEndpointLocked(src)
		if !ok {
			t.logf("OUT  -> REJECT port-exhausted: no free port in [%d,%d]", t.lo, t.hi)
			return Outcome{}, ErrPortPoolExhausted
		}
		c = &conn{
			key:      key,
			extPort:  extPort,
			state:    StateHalfOpen,
			expireAt: now + t.th,
		}
		t.indexConnLocked(c)
		t.lastTime, t.timeSet = now, true
		t.logf("OUT  -> CREATE ext=%d state=%s expire=%d (basis: outbound SYN -> half-open, now+Th)",
			extPort, c.state, c.expireAt)
		return t.outcomeLocked(c, true, false), nil
	}

	t.applyPacketLocked(c, now, kind, "OUT")
	t.lastTime = now
	return t.outcomeLocked(c, false, kind == PktRST), nil
}

// ProcessInbound 处理一个入站事件。extPort 为报文目的外部端口，
// remote 为发送该回包的远端端点。
func (t *Table) ProcessInbound(now int64, extPort uint16, remote Endpoint, kind PacketKind) (Outcome, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.logf("IN   now=%d ext=%d remote=%s:%d kind=%s", now, extPort, remote.Addr, remote.Port, kind)

	if t.timeSet && now < t.lastTime {
		t.logf("IN   -> REJECT clock-rewind: now=%d < last=%d", now, t.lastTime)
		return Outcome{}, ErrClockRewind
	}

	if _, ok := t.extOwner[extPort]; !ok {
		t.logf("IN   -> REJECT no-mapping: external port %d has no owner", extPort)
		return Outcome{}, ErrNoMapping
	}

	var c *conn
	if byRemote, ok := t.extIndex[extPort]; ok {
		if key, ok2 := byRemote[remote]; ok2 {
			c = t.conns[key]
		}
	}
	if c == nil || c.expireAt <= now {
		t.logf("IN   -> REJECT no-connection: ext=%d has owner but no live connection from remote %s:%d",
			extPort, remote.Addr, remote.Port)
		return Outcome{}, ErrNoConnection
	}

	// 接受：回收其他到期条目（不会影响本条存活连接）。
	t.sweepLocked(now)
	t.applyPacketLocked(c, now, kind, "IN")
	t.lastTime = now
	return t.outcomeLocked(c, false, kind == PktRST), nil
}

// Lookup 按四元组查询连接；不存在或已到期时 ok 为 false。查询不改变任何状态。
func (t *Table) Lookup(key ConnKey, now int64) (ConnInfo, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	c, ok := t.conns[key]
	if !ok || c.expireAt <= now {
		t.logf("LOOKUP now=%d key=%s:%d<->%s:%d -> MISS", now,
			key.Src.Addr, key.Src.Port, key.Dst.Addr, key.Dst.Port)
		return ConnInfo{}, false
	}
	t.logf("LOOKUP now=%d key=%s:%d<->%s:%d -> HIT ext=%d state=%s expire=%d",
		now, key.Src.Addr, key.Src.Port, key.Dst.Addr, key.Dst.Port,
		c.extPort, c.state, c.expireAt)
	return ConnInfo{Key: c.key, ExtPort: c.extPort, State: c.state, ExpireAt: c.expireAt}, true
}

// Stats 是表当前规模的快照。
type Stats struct {
	Connections int
	UsedPorts   int
}

// Stats 返回当前活动连接数与已占用外部端口数。
func (t *Table) Stats(now int64) Stats {
	t.mu.Lock()
	defer t.mu.Unlock()

	var s Stats
	livePorts := make(map[uint16]struct{})
	for _, c := range t.conns {
		if c.expireAt > now {
			s.Connections++
			livePorts[c.extPort] = struct{}{}
		}
	}
	s.UsedPorts = len(livePorts)
	return s
}

// sweepLocked 删除所有到期（expireAt <= now）的连接，
// 并在内部端点失去最后一条连接时释放其外部端口。
func (t *Table) sweepLocked(now int64) {
	for key, c := range t.conns {
		if c.expireAt <= now {
			t.removeConnLocked(c)
			t.logf("SWEEP expire key=%s:%d<->%s:%d ext=%d expired-at=%d <= now=%d",
				key.Src.Addr, key.Src.Port, key.Dst.Addr, key.Dst.Port, c.extPort, c.expireAt, now)
		}
	}
}

// portForEndpointLocked 返回内部端点应使用的外部端口：
// 已有连接则共用，否则取闭区间 [lo,hi] 中最小的空闲端口。
func (t *Table) portForEndpointLocked(src Endpoint) (uint16, bool) {
	if set := t.ownerConns[src]; len(set) > 0 {
		for key := range set {
			if c, ok := t.conns[key]; ok {
				return c.extPort, true
			}
		}
	}
	for port := t.lo; ; port++ {
		if _, used := t.extOwner[port]; !used {
			t.extOwner[port] = src
			t.logf("PORT  allocate smallest-free port=%d for endpoint=%s:%d", port, src.Addr, src.Port)
			return port, true
		}
		if port == t.hi {
			return 0, false
		}
	}
}

func (t *Table) indexConnLocked(c *conn) {
	t.conns[c.key] = c
	src := c.key.Src
	set, ok := t.ownerConns[src]
	if !ok {
		set = make(map[ConnKey]struct{})
		t.ownerConns[src] = set
	}
	set[c.key] = struct{}{}
	byRemote, ok := t.extIndex[c.extPort]
	if !ok {
		byRemote = make(map[Endpoint]ConnKey)
		t.extIndex[c.extPort] = byRemote
	}
	byRemote[c.key.Dst] = c.key
}

// removeConnLocked 摘除连接的全部索引；端点最后一条连接消失时释放端口。
func (t *Table) removeConnLocked(c *conn) {
	delete(t.conns, c.key)
	src := c.key.Src
	if set := t.ownerConns[src]; set != nil {
		delete(set, c.key)
		if len(set) == 0 {
			delete(t.ownerConns, src)
			delete(t.extOwner, c.extPort)
			delete(t.extIndex, c.extPort)
			t.logf("PORT  release port=%d: endpoint=%s:%d has no live connection left",
				c.extPort, src.Addr, src.Port)
		} else if byRemote := t.extIndex[c.extPort]; byRemote != nil {
			delete(byRemote, c.key.Dst)
		}
	}
}

// applyPacketLocked 对一条已存在的存活连接施加状态迁移。
// RST 立即删除；FIN 使非关闭连接进入关闭；放行报文按状态刷新到期。
func (t *Table) applyPacketLocked(c *conn, now int64, kind PacketKind, dir string) {
	switch kind {
	case PktRST:
		t.removeConnLocked(c)
		t.logf("%s -> DELETE ext=%d (basis: RST removes immediately)", dir, c.extPort)
	case PktFIN:
		if c.state != StateClosed {
			old := c.state
			c.state = StateClosed
			c.expireAt = now + t.tc
			t.logf("%s -> ext=%d %s->CLOSED expire=%d (basis: FIN, now+Tc)", dir, c.extPort, old, c.expireAt)
		} else {
			t.logf("%s -> ext=%d CLOSED packet allowed, expire unchanged=%d", dir, c.extPort, c.expireAt)
		}
	default: // SYN 或 DATA
		switch c.state {
		case StateHalfOpen:
			if dir == "IN" {
				// 仅入站 SYN/DATA 可使半开连接变为已建立。
				c.state = StateEstablished
				c.expireAt = now + t.te
				t.logf("%s -> ext=%d HALF_OPEN->ESTABLISHED expire=%d (basis: inbound %s, now+Te)",
					dir, c.extPort, c.expireAt, kind)
			} else {
				c.expireAt = now + t.th
				t.logf("%s -> ext=%d HALF_OPEN refresh expire=%d (basis: outbound %s, now+Th)",
					dir, c.extPort, c.expireAt, kind)
			}
		case StateEstablished:
			c.expireAt = now + t.te
			t.logf("%s -> ext=%d ESTABLISHED refresh expire=%d (basis: allowed %s, now+Te)",
				dir, c.extPort, c.expireAt, kind)
		case StateClosed:
			t.logf("%s -> ext=%d CLOSED packet allowed, expire unchanged=%d", dir, c.extPort, c.expireAt)
		}
	}
}

func (t *Table) outcomeLocked(c *conn, created, deleted bool) Outcome {
	if deleted {
		return Outcome{Allowed: true, ExtPort: c.extPort, State: c.state, Deleted: true}
	}
	return Outcome{
		Allowed:  true,
		ExtPort:  c.extPort,
		State:    c.state,
		ExpireAt: c.expireAt,
		Created:  created,
	}
}

func (t *Table) logf(format string, args ...any) {
	if t.logger != nil {
		t.logger.Printf(format, args...)
	}
}

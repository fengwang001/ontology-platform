// Package registry 实现带遗嘱消息（Last Will）的客户端连接登记表。
//
// 所有时间均由调用方以毫秒整数传入；登记表自身不读取系统时钟，
// 因此同一段调用序列可以被确定性地重放。
package registry

import (
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
)

// Will 是客户端遗嘱：异常断线后延迟 DelayMillis 毫秒发布。
type Will struct {
	Topic       string
	Payload     string
	DelayMillis int64
}

// Publish 是一条已经发布的遗嘱记录。
type Publish struct {
	Client      string
	Topic       string
	Payload     string
	PublishedAt int64
}

// ErrorCode 标识操作被整体拒绝的可区分原因。
type ErrorCode int

const (
	// ErrClockWentBack：now 小于此前任一次调用传入的 now。
	ErrClockWentBack ErrorCode = iota + 1
	// ErrEmptyClientID：客户端标识为空。
	ErrEmptyClientID
	// ErrNegativeKeepAlive：保活秒数 K 为负。
	ErrNegativeKeepAlive
	// ErrNegativeDelay：遗嘱延迟毫秒 D 为负。
	ErrNegativeDelay
	// ErrClientNotOnline：活动或断开的客户端当前不在线。
	ErrClientNotOnline
)

// CallError 携带可区分的拒绝原因。
type CallError struct {
	Code ErrorCode
	msg  string
}

func (e *CallError) Error() string { return e.msg }

func callError(code ErrorCode, format string, args ...any) *CallError {
	return &CallError{Code: code, msg: fmt.Sprintf(format, args...)}
}

type connection struct {
	keepAliveSec int64
	lastActive   int64
	will         *Will
}

type pendingWill struct {
	topic         string
	payload       string
	dueAt         int64
	disconnectSeq uint64
}

// Registry 是并发安全的客户端连接登记表。
type Registry struct {
	mu        sync.Mutex
	conns     map[string]*connection
	pending   map[string]*pendingWill
	published []Publish
	lastNow   int64
	logger    Logger
	discoSeq  uint64
}

// Logger 记录每次调用的输入、输出与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

type stdLogger struct{ l *log.Logger }

func (s stdLogger) Logf(format string, args ...any) { s.l.Printf(format, args...) }

// New 创建一个空登记表。
func New(logger Logger) *Registry {
	if logger == nil {
		logger = stdLogger{l: log.New(os.Stderr, "[registry] ", log.LstdFlags|log.Lmicroseconds)}
	}
	return &Registry{
		conns:   make(map[string]*connection),
		pending: make(map[string]*pendingWill),
		logger:  logger,
	}
}

func (r *Registry) logf(format string, args ...any) { r.logger.Logf(format, args...) }

// entryProcess 是每个操作执行自身逻辑前的入口处理：
// 先按客户端标识升序判定全部保活超时，再发布所有计划时刻不大于 now 的待发布遗嘱。
// 调用方须持有 r.mu。
func (r *Registry) entryProcess(now int64) []Publish {
	r.lastNow = now

	ids := make([]string, 0, len(r.conns))
	for id := range r.conns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		conn := r.conns[id]
		if conn == nil {
			continue
		}
		if conn.keepAliveSec > 0 && now-conn.lastActive > 1500*conn.keepAliveSec {
			r.markAbnormalDisconnect(id, conn, now, "keepalive-timeout")
		}
	}

	return r.publishDue(now)
}

// markAbnormalDisconnect 处理一次异常断线：移除在线连接，有遗嘱则加入等待发布。
// 断线时刻取发现它的那次调用的 now。调用方须持有 r.mu。
func (r *Registry) markAbnormalDisconnect(id string, conn *connection, now int64, reason string) {
	r.discoSeq++
	seq := r.discoSeq
	delete(r.conns, id)
	if conn.will == nil {
		r.logf("disconnect abnormal: client=%q at=%d reason=%s will=none (void)", id, now, reason)
		return
	}
	r.pending[id] = &pendingWill{
		topic:         conn.will.Topic,
		payload:       conn.will.Payload,
		dueAt:         now + conn.will.DelayMillis,
		disconnectSeq: seq,
	}
	r.logf("disconnect abnormal: client=%q at=%d reason=%s will_scheduled due_at=%d delay_ms=%d seq=%d",
		id, now, reason, now+conn.will.DelayMillis, conn.will.DelayMillis, seq)
}

// publishDue 发布所有计划时刻不大于 now 的遗嘱，按（计划时刻，断线先后序）升序。
// 发布时记录的 now 为本次调用传入的 now。调用方须持有 r.mu。
func (r *Registry) publishDue(now int64) []Publish {
	due := make([]string, 0, len(r.pending))
	for id, pw := range r.pending {
		if pw.dueAt <= now {
			due = append(due, id)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		a, b := r.pending[due[i]], r.pending[due[j]]
		if a.dueAt != b.dueAt {
			return a.dueAt < b.dueAt
		}
		return a.disconnectSeq < b.disconnectSeq
	})
	out := make([]Publish, 0, len(due))
	for _, id := range due {
		pw := r.pending[id]
		rec := Publish{Client: id, Topic: pw.topic, Payload: pw.payload, PublishedAt: now}
		delete(r.pending, id)
		r.published = append(r.published, rec)
		out = append(out, rec)
		r.logf("publish: client=%q topic=%q payload=%q due_at=%d published_at=%d seq=%d",
			id, pw.topic, pw.payload, pw.dueAt, now, pw.disconnectSeq)
	}
	return out
}

// Connect 登记一条客户端连接。
// 同标识已在线时视为接管：旧连接被立即替换，旧遗嘱作废，等待发布中的遗嘱一并取消。
func (r *Registry) Connect(clientID string, keepAliveSec int64, will *Will, now int64) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logf("input Connect: client=%q keepalive_sec=%d will=%v now=%d", clientID, keepAliveSec, will, now)
	defer func() {
		if err != nil {
			r.logf("output Connect: client=%q rejected=%s", clientID, err)
		} else {
			r.logf("output Connect: client=%q ok now=%d", clientID, now)
		}
	}()

	if now < r.lastNow {
		return callError(ErrClockWentBack,
			"clock went back: now=%d < last_now=%d", now, r.lastNow)
	}
	if clientID == "" {
		return callError(ErrEmptyClientID, "empty client id: now=%d", now)
	}
	if keepAliveSec < 0 {
		return callError(ErrNegativeKeepAlive,
			"negative keepalive %d: client=%q", keepAliveSec, clientID)
	}
	if will != nil && will.DelayMillis < 0 {
		return callError(ErrNegativeDelay,
			"negative will delay %d: client=%q", will.DelayMillis, clientID)
	}

	published := r.entryProcess(now)
	if len(published) > 0 {
		r.logf("Connect entry: now=%d published=%d", now, len(published))
	}

	if old := r.conns[clientID]; old != nil {
		delete(r.conns, clientID)
		r.logf("takeover: client=%q old connection replaced at=%d old_will_void=true", clientID, now)
	}
	if old := r.pending[clientID]; old != nil {
		delete(r.pending, clientID)
		r.logf("takeover: client=%q pending will canceled at=%d due_at=%d", clientID, now, old.dueAt)
	}
	r.conns[clientID] = &connection{
		keepAliveSec: keepAliveSec,
		lastActive:   now,
		will:         will,
	}
	r.logf("decision Connect: client=%q online last_active=%d (connect itself counts as activity)", clientID, now)
	return nil
}

// Active 记录一次客户端活动。
func (r *Registry) Active(clientID string, now int64) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logf("input Active: client=%q now=%d", clientID, now)
	defer func() {
		if err != nil {
			r.logf("output Active: client=%q rejected=%s", clientID, err)
		} else {
			r.logf("output Active: client=%q ok now=%d", clientID, now)
		}
	}()

	if now < r.lastNow {
		return callError(ErrClockWentBack,
			"clock went back: now=%d < last_now=%d", now, r.lastNow)
	}
	if clientID == "" {
		return callError(ErrEmptyClientID, "empty client id: now=%d", now)
	}

	published := r.entryProcess(now)
	if len(published) > 0 {
		r.logf("Active entry: now=%d published=%d", now, len(published))
	}

	conn := r.conns[clientID]
	if conn == nil {
		return callError(ErrClientNotOnline,
			"client %q not online at now=%d (entry effects retained)", clientID, now)
	}
	conn.lastActive = now
	r.logf("decision Active: client=%q last_active=%d", clientID, now)
	return nil
}

// Disconnect 断开客户端；normal 为真表示正常断开。
func (r *Registry) Disconnect(clientID string, normal bool, now int64) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logf("input Disconnect: client=%q normal=%v now=%d", clientID, normal, now)
	defer func() {
		if err != nil {
			r.logf("output Disconnect: client=%q rejected=%s", clientID, err)
		} else {
			r.logf("output Disconnect: client=%q ok normal=%v now=%d", clientID, normal, now)
		}
	}()

	if now < r.lastNow {
		return callError(ErrClockWentBack,
			"clock went back: now=%d < last_now=%d", now, r.lastNow)
	}
	if clientID == "" {
		return callError(ErrEmptyClientID, "empty client id: now=%d", now)
	}

	published := r.entryProcess(now)
	if len(published) > 0 {
		r.logf("Disconnect entry: now=%d published=%d", now, len(published))
	}

	conn := r.conns[clientID]
	if conn == nil {
		return callError(ErrClientNotOnline,
			"client %q not online at now=%d (entry effects retained)", clientID, now)
	}

	delete(r.conns, clientID)
	if normal {
		r.logf("decision Disconnect: client=%q normal at=%d will_void=true", clientID, now)
		return nil
	}
	r.markAbnormalDisconnect(clientID, conn, now, "explicit-disconnect")
	return nil
}

// Advance 推进时钟，发布所有到期遗嘱。
func (r *Registry) Advance(now int64) (out []Publish, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logf("input Advance: now=%d", now)
	defer func() {
		if err != nil {
			r.logf("output Advance: rejected=%s", err)
		} else {
			r.logf("output Advance: now=%d published=%d", now, len(out))
		}
	}()

	if now < r.lastNow {
		return nil, callError(ErrClockWentBack,
			"clock went back: now=%d < last_now=%d", now, r.lastNow)
	}

	out = r.entryProcess(now)
	return out, nil
}

// Published 返回已发布遗嘱记录的快照（按发布先后）。
func (r *Registry) Published() []Publish {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Publish, len(r.published))
	copy(out, r.published)
	return out
}

// DiscardLogger 丢弃全部日志输出，可用于不关心日志的场景。
type DiscardLogger struct{}

// Logf 实现 Logger。
func (DiscardLogger) Logf(string, ...any) {}

package ontology

import "context"

// 读取请求的种类。
const (
	readNone     = 0
	readSnapshot = 1
	readLog      = 2
	readRows     = 3
)

// Aggregator 是增量分组聚合器：随行的插入、更新、删除实时维护每组的
// 求和与计数，并按“先撤回旧值、再写入新值；改键先旧组后新组”的顺序
// 输出净变化日志。写入与读取均可并发调用。
//
// 内部采用单 goroutine actor：所有状态访问在 actor 循环内串行执行，
// 调用方阻塞等待结果，因此无需对 engine 加锁，也天然避免读—写竞态。
type Aggregator struct {
	requests chan request
	done     chan struct{}
}

// request 是发往 actor 循环的一条消息。
//
//	isApply                —— Apply 一批操作（空批次是合法的 no-op）
//	read  != readNone      —— 一次纯读取
//	sub   != nil           —— 注册日志订阅者（历史回放 + 后续推送均在 actor 内）
type request struct {
	ops     []Op
	isApply bool
	read    int
	sub     *subscriber
	reply   chan response
}

// response 是 actor 对一次请求的应答（按请求类型只取相应字段）。
type response struct {
	entries []LogEntry
	view    []GroupView
	log     []LogEntry
	rows    map[string]Row
	err     error
}

// subscriber 是一个日志订阅者。
type subscriber struct {
	ctx context.Context
	ch  chan<- LogEntry
}

// New 创建聚合器。maxGroups <= 0 表示不限制组数。
func New(maxGroups int) *Aggregator {
	a := &Aggregator{
		requests: make(chan request),
		done:     make(chan struct{}),
	}
	go a.loop(newEngine(maxGroups))
	return a
}

// loop 是唯一持有 engine 的 goroutine。
func (a *Aggregator) loop(eng *engine) {
	defer close(a.done)

	var subs []*subscriber
	for req := range a.requests {
		// 顺手摘除已取消的订阅者，避免其在无新日志期间长期滞留。
		subs = pruneCancelled(subs)

		switch {
		case req.sub != nil:
			// 注册订阅：先在 actor 内完整回放历史日志，再加入订阅列表。
			// 回放与注册之间不会插入任何写入，因此不重不漏。
			if a.deliver(req.sub, eng.logView()) {
				subs = append(subs, req.sub)
			}

		case req.isApply:
			entries, err := eng.apply(req.ops)
			if err == nil {
				// 仅提交成功的批次才推送；被拒绝的批次不产生任何日志。
				a.deliverAll(&subs, entries)
			}
			req.reply <- response{entries: entries, err: err}

		case req.read == readSnapshot:
			req.reply <- response{view: eng.snapshot()}
		case req.read == readLog:
			req.reply <- response{log: eng.logView()}
		case req.read == readRows:
			req.reply <- response{rows: eng.rowsView()}
		}
	}
}

// pruneCancelled 丢弃 ctx 已结束的订阅者。
func pruneCancelled(subs []*subscriber) []*subscriber {
	alive := subs[:0]
	for _, s := range subs {
		if s.ctx.Err() == nil {
			alive = append(alive, s)
		}
	}
	return alive
}

// deliver 向单个订阅者按序投递 entries。
// 订阅者 ctx 取消时中止并返回 false。
// 通道缓冲满时阻塞在发送上——这会阻塞 actor，从而对 Apply 形成背压。
func (a *Aggregator) deliver(s *subscriber, entries []LogEntry) bool {
	for _, en := range entries {
		select {
		case s.ch <- en:
		case <-s.ctx.Done():
			return false
		}
	}
	return true
}

// deliverAll 向全部订阅者投递本批新日志，中途取消的订阅者被摘除。
func (a *Aggregator) deliverAll(subs *[]*subscriber, entries []LogEntry) {
	if len(entries) == 0 || len(*subs) == 0 {
		return
	}
	alive := (*subs)[:0]
	for _, s := range *subs {
		if a.deliver(s, entries) {
			alive = append(alive, s)
		}
	}
	*subs = alive
}

// Apply 原子地应用一批操作。
//
// 批次中只要存在一条非法操作，整批拒绝：行表、聚合与日志均不发生任何变化，
// 返回 *RejectError（可用 errors.Is 判定具体原因）。成功时返回本批产生的、
// 已按全局序号排好序的日志条目。可与读取及其他写入并发调用（写入间串行）。
func (a *Aggregator) Apply(ops []Op) ([]LogEntry, error) {
	reply := make(chan response, 1)
	a.requests <- request{ops: ops, isApply: true, reply: reply}
	res := <-reply
	return res.entries, res.err
}

// read 发送一次纯读取请求。
func (a *Aggregator) read(kind int) response {
	reply := make(chan response, 1)
	a.requests <- request{read: kind, reply: reply}
	return <-reply
}

// Snapshot 返回当前所有计数为正的分组聚合视图，按分组键排序。
// 可与写入及其他读取并发调用，结果与批量重算一致。
func (a *Aggregator) Snapshot() []GroupView {
	return a.read(readSnapshot).view
}

// Log 返回自创建以来全部已提交日志条目的有序副本。
func (a *Aggregator) Log() []LogEntry {
	return a.read(readLog).log
}

// Rows 返回行表当前内容的副本（rowID -> Row），主要用于测试与校验。
func (a *Aggregator) Rows() map[string]Row {
	return a.read(readRows).rows
}

// Stream 按顺序向 ch 回放全部历史日志，并在此后持续推送每条新提交的日志，
// 直到 ctx 取消。每个条目恰好投递一次，序号全局连续。
//
// 注册（含历史回放）在 actor 内完成，回放与实时推送之间不会穿插写入，
// 因此不存在丢条目窗口。ch 缓冲满时推送阻塞 actor 并进而阻塞 Apply，
// 形成天然背压；调用方应在调用 Stream 的同时并发读取 ch。
func (a *Aggregator) Stream(ctx context.Context, ch chan<- LogEntry) {
	a.requests <- request{sub: &subscriber{ctx: ctx, ch: ch}}
}

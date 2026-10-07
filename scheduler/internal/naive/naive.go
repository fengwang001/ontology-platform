// Package naive 是调度器语义的独立朴素参考实现，仅用于随机对照测试。
//
// 其实现刻意保持“显然正确”：待发队列与在途列表均用普通切片加线性
// 扫描，不使用任何增量数据结构，单次事件代价允许随队列长度增长。
// 它与生产实现共享全部可观察语义（事件输出、错误分类、状态快照），
// 但两段代码独立编写，以便通过大规模随机对照发现实现偏差。
package naive

import (
	"fmt"
	"sort"

	"ontology/scheduler"
)

type seg struct {
	seq      int64
	len      int64
	excluded string
}

type subflow struct {
	id            string
	rtt           int64
	cwnd          int64
	initialCwnd   int64
	role          scheduler.Role
	active        bool
	ackedBytes    int64
	sentBytes     int64
	inflightBytes int64
	inflight      []seg
}

// Model 是朴素参考模型，API 与 scheduler.Scheduler 对应。
type Model struct {
	m        int64
	nextSeq  int64
	sentMax  int64
	acked    int64
	window   int64
	subs     map[string]*subflow
	reinj    []seg // 始终按 seq 升序
	fresh    []seg
	decision []scheduler.SentSegment
}

// New 创建参考模型。
func New(m int64) (*Model, error) {
	if m <= 0 {
		return nil, &scheduler.Error{Code: scheduler.ErrInvalidArgument,
			Detail: fmt.Sprintf("最大段长必须为正整数，得到 %d", m)}
	}
	return &Model{m: m, subs: make(map[string]*subflow)}, nil
}

func reject(code scheduler.ErrorCode, format string, args ...any) ([]scheduler.SentSegment, error) {
	return nil, &scheduler.Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// activeNormal 报告是否存在活跃的普通子流（线性扫描）。
func (mo *Model) activeNormal() bool {
	for _, sub := range mo.subs {
		if sub.active && sub.role == scheduler.Normal {
			return true
		}
	}
	return false
}

// pick 线性扫描全部子流，按规则选出最佳者。
func (mo *Model) pick(need int64, excluded string) *subflow {
	want := scheduler.Normal
	if !mo.activeNormal() {
		want = scheduler.Backup
	}
	var best *subflow
	for _, sub := range mo.subs {
		if !sub.active || sub.role != want || sub.id == excluded {
			continue
		}
		if sub.cwnd-sub.inflightBytes < need {
			continue
		}
		if best == nil || sub.rtt < best.rtt || (sub.rtt == best.rtt && sub.id < best.id) {
			best = sub
		}
	}
	return best
}

// transmit 反复扫描队首直到无法继续发送。
func (mo *Model) transmit() {
	for {
		if len(mo.reinj) > 0 {
			rs := mo.reinj[0]
			sub := mo.pick(rs.len, rs.excluded)
			if sub == nil {
				return
			}
			mo.reinj = mo.reinj[1:]
			mo.send(sub, rs, true)
			continue
		}
		if len(mo.fresh) == 0 {
			return
		}
		fs := mo.fresh[0]
		if fs.seq+fs.len > mo.acked+mo.window {
			return
		}
		sub := mo.pick(fs.len, "")
		if sub == nil {
			return
		}
		mo.fresh = mo.fresh[1:]
		mo.send(sub, fs, false)
	}
}

func (mo *Model) send(sub *subflow, g seg, resend bool) {
	sub.inflight = append(sub.inflight, seg{seq: g.seq, len: g.len})
	sub.inflightBytes += g.len
	sub.sentBytes += g.len
	if !resend {
		mo.sentMax = g.seq + g.len
	}
	mo.decision = append(mo.decision, scheduler.SentSegment{
		Seq: g.seq, Len: g.len, Subflow: sub.id, Resend: resend,
	})
}

// finish 在事件被接受后执行发送决策并返回本次发出的段。
func (mo *Model) finish() []scheduler.SentSegment {
	mo.decision = mo.decision[:0]
	mo.transmit()
	out := make([]scheduler.SentSegment, len(mo.decision))
	copy(out, mo.decision)
	return out
}

// AddSubflow 见 scheduler.Scheduler.AddSubflow。
func (mo *Model) AddSubflow(id string, rtt int64, cwnd int64, role scheduler.Role) ([]scheduler.SentSegment, error) {
	if id == "" {
		return reject(scheduler.ErrInvalidArgument, "子流标识不能为空")
	}
	if rtt <= 0 {
		return reject(scheduler.ErrInvalidArgument, "往返时延必须为正")
	}
	if cwnd < mo.m {
		return reject(scheduler.ErrInvalidArgument, "拥塞窗口小于最大段长")
	}
	if role != scheduler.Normal && role != scheduler.Backup {
		return reject(scheduler.ErrInvalidArgument, "角色非法")
	}
	if _, dup := mo.subs[id]; dup {
		return reject(scheduler.ErrInvalidArgument, "子流已存在")
	}
	mo.subs[id] = &subflow{id: id, rtt: rtt, cwnd: cwnd, initialCwnd: cwnd, role: role, active: true}
	return mo.finish(), nil
}

// Write 见 scheduler.Scheduler.Write。
func (mo *Model) Write(n int64) ([]scheduler.SentSegment, error) {
	if n <= 0 {
		return reject(scheduler.ErrInvalidArgument, "写入字节数必须为正")
	}
	for n > 0 {
		l := n
		if l > mo.m {
			l = mo.m
		}
		mo.fresh = append(mo.fresh, seg{seq: mo.nextSeq, len: l})
		mo.nextSeq += l
		n -= l
	}
	return mo.finish(), nil
}

// SubflowAck 见 scheduler.Scheduler.SubflowAck。
func (mo *Model) SubflowAck(id string, cum int64) ([]scheduler.SentSegment, error) {
	if id == "" {
		return reject(scheduler.ErrInvalidArgument, "子流标识不能为空")
	}
	if cum < 0 {
		return reject(scheduler.ErrInvalidArgument, "累计确认字节数不能为负")
	}
	sub, ok := mo.subs[id]
	if !ok {
		return reject(scheduler.ErrSubflowNotFound, "子流不存在")
	}
	if !sub.active {
		return mo.finish(), nil // 迟到确认，忽略
	}
	if cum < sub.ackedBytes {
		return reject(scheduler.ErrStaleAck, "累计确认倒退")
	}
	newly := cum - sub.ackedBytes
	if newly > sub.inflightBytes {
		return reject(scheduler.ErrAckOutOfRange, "确认超出在途")
	}
	rem := newly
	idx := 0
	for rem > 0 {
		g := sub.inflight[idx]
		if g.len > rem {
			return reject(scheduler.ErrAckMisaligned, "确认未落在段边界上")
		}
		rem -= g.len
		idx++
	}
	sub.inflight = sub.inflight[idx:]
	sub.inflightBytes -= newly
	sub.ackedBytes = cum
	return mo.finish(), nil
}

// ConnAck 见 scheduler.Scheduler.ConnAck。
func (mo *Model) ConnAck(ackSeq int64, window int64) ([]scheduler.SentSegment, error) {
	if ackSeq < 0 {
		return reject(scheduler.ErrInvalidArgument, "确认序号不能为负")
	}
	if window < 0 {
		return reject(scheduler.ErrInvalidArgument, "通告窗口不能为负")
	}
	if ackSeq < mo.acked {
		return reject(scheduler.ErrStaleAck, "确认序号倒退")
	}
	if ackSeq > mo.sentMax {
		return reject(scheduler.ErrAckOutOfRange, "确认超出已发送范围")
	}
	mo.acked = ackSeq
	mo.window = window
	keep := mo.reinj[:0]
	for _, g := range mo.reinj {
		if g.seq+g.len > mo.acked {
			keep = append(keep, g)
		}
	}
	mo.reinj = keep
	return mo.finish(), nil
}

// SubflowFail 见 scheduler.Scheduler.SubflowFail。
func (mo *Model) SubflowFail(id string) ([]scheduler.SentSegment, error) {
	if id == "" {
		return reject(scheduler.ErrInvalidArgument, "子流标识不能为空")
	}
	sub, ok := mo.subs[id]
	if !ok {
		return reject(scheduler.ErrSubflowNotFound, "子流不存在")
	}
	if !sub.active {
		return mo.finish(), nil
	}
	sub.active = false
	for _, g := range sub.inflight {
		if g.seq+g.len <= mo.acked {
			continue
		}
		dup := false
		for _, r := range mo.reinj {
			if r.seq == g.seq {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		// 线性插入，保持升序。
		pos := len(mo.reinj)
		for i, r := range mo.reinj {
			if r.seq > g.seq {
				pos = i
				break
			}
		}
		mo.reinj = append(mo.reinj, seg{})
		copy(mo.reinj[pos+1:], mo.reinj[pos:])
		mo.reinj[pos] = seg{seq: g.seq, len: g.len, excluded: id}
	}
	sub.inflight = nil
	sub.inflightBytes = 0
	return mo.finish(), nil
}

// SubflowRecover 见 scheduler.Scheduler.SubflowRecover。
func (mo *Model) SubflowRecover(id string) ([]scheduler.SentSegment, error) {
	if id == "" {
		return reject(scheduler.ErrInvalidArgument, "子流标识不能为空")
	}
	sub, ok := mo.subs[id]
	if !ok {
		return reject(scheduler.ErrSubflowNotFound, "子流不存在")
	}
	if sub.active {
		return mo.finish(), nil
	}
	sub.active = true
	sub.cwnd = sub.initialCwnd
	return mo.finish(), nil
}

// SetRTT 见 scheduler.Scheduler.SetRTT。
func (mo *Model) SetRTT(id string, rtt int64) ([]scheduler.SentSegment, error) {
	if id == "" {
		return reject(scheduler.ErrInvalidArgument, "子流标识不能为空")
	}
	if rtt <= 0 {
		return reject(scheduler.ErrInvalidArgument, "往返时延必须为正")
	}
	sub, ok := mo.subs[id]
	if !ok {
		return reject(scheduler.ErrSubflowNotFound, "子流不存在")
	}
	sub.rtt = rtt
	return mo.finish(), nil
}

// Snapshot 返回与 scheduler.Scheduler.Snapshot 可比对的状态。
func (mo *Model) Snapshot() scheduler.State {
	st := scheduler.State{
		NextSeq: mo.nextSeq,
		SentMax: mo.sentMax,
		Acked:   mo.acked,
		Window:  mo.window,
	}
	for _, g := range mo.reinj {
		st.PendingReinject = append(st.PendingReinject, scheduler.Seg{Seq: g.seq, Len: g.len, Excluded: g.excluded})
	}
	for _, g := range mo.fresh {
		st.PendingFresh = append(st.PendingFresh, scheduler.Seg{Seq: g.seq, Len: g.len})
	}
	ids := make([]string, 0, len(mo.subs))
	for id := range mo.subs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		sub := mo.subs[id]
		ss := scheduler.SubflowState{
			ID:            sub.id,
			RTT:           sub.rtt,
			Cwnd:          sub.cwnd,
			InitialCwnd:   sub.initialCwnd,
			Role:          sub.role,
			Active:        sub.active,
			AckedBytes:    sub.ackedBytes,
			SentBytes:     sub.sentBytes,
			InflightBytes: sub.inflightBytes,
		}
		for _, g := range sub.inflight {
			ss.Inflight = append(ss.Inflight, scheduler.Seg{Seq: g.seq, Len: g.len})
		}
		st.Subflows = append(st.Subflows, ss)
	}
	return st
}

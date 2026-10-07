package scheduler

import (
	"errors"
	"fmt"
	"sort"
)

func (c ErrorCode) String() string {
	switch c {
	case ErrInvalidArgument:
		return "参数非法"
	case ErrSubflowNotFound:
		return "子流不存在"
	case ErrStaleAck:
		return "过期确认"
	case ErrAckMisaligned:
		return "确认非整段"
	case ErrAckOutOfRange:
		return "确认越界"
	}
	return "未知错误"
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

// CodeOf 提取错误的分类码；非调度器错误返回 ok=false。
func CodeOf(err error) (code ErrorCode, ok bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, true
	}
	return 0, false
}

func reject(code ErrorCode, format string, args ...any) (*Decision, error) {
	return nil, &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// New 创建调度器，m 为最大段长（正整数）。
func New(m int64) (*Scheduler, error) {
	if m <= 0 {
		return nil, &Error{Code: ErrInvalidArgument, Detail: fmt.Sprintf("最大段长必须为正整数，得到 %d", m)}
	}
	s := &Scheduler{m: m, subs: make(map[string]*subflow)}
	s.reinj.visits = &s.stats.TreapNodeVisits
	return s, nil
}

// AddSubflow 添加子流。id 非空且唯一；rtt 为正（毫秒）；cwnd 不小于 M。
func (s *Scheduler) AddSubflow(id string, rtt int64, cwnd int64, role Role) (*Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return reject(ErrInvalidArgument, "子流标识不能为空")
	}
	if rtt <= 0 {
		return reject(ErrInvalidArgument, "子流 %q 的往返时延必须为正，得到 %d", id, rtt)
	}
	if cwnd < s.m {
		return reject(ErrInvalidArgument, "子流 %q 的拥塞窗口 %d 小于最大段长 %d", id, cwnd, s.m)
	}
	if role != Normal && role != Backup {
		return reject(ErrInvalidArgument, "子流 %q 的角色非法：%d", id, role)
	}
	if _, dup := s.subs[id]; dup {
		return reject(ErrInvalidArgument, "子流 %q 已存在", id)
	}
	s.subs[id] = &subflow{
		id: id, rtt: rtt, cwnd: cwnd, initialCwnd: cwnd,
		role: role, active: true,
	}
	if role == Normal {
		s.activeNormal++
	}
	return s.finish(), nil
}

// Write 写入 n 字节数据，按连接级序号切段后进入待发队列。
func (s *Scheduler) Write(n int64) (*Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 {
		return reject(ErrInvalidArgument, "写入字节数必须为正，得到 %d", n)
	}
	for n > 0 {
		l := n
		if l > s.m {
			l = s.m
		}
		s.fresh.push(seg{seq: s.nextSeq, len: l})
		s.stats.FreshPushes++
		s.stats.SegmentsWritten++
		s.nextSeq += l
		n -= l
	}
	return s.finish(), nil
}

// SubflowAck 子流确认：cum 为该子流按发送顺序累计确认的字节数。
// 失效子流的确认视为迟到，忽略且不报错。
func (s *Scheduler) SubflowAck(id string, cum int64) (*Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return reject(ErrInvalidArgument, "子流标识不能为空")
	}
	if cum < 0 {
		return reject(ErrInvalidArgument, "累计确认字节数不能为负，得到 %d", cum)
	}
	sub, ok := s.subs[id]
	if !ok {
		return reject(ErrSubflowNotFound, "子流 %q 不存在", id)
	}
	if !sub.active {
		// 迟到确认：忽略，不改变任何状态。
		return s.finish(), nil
	}
	if cum < sub.ackedBytes {
		return reject(ErrStaleAck, "子流 %q 的累计确认 %d 小于已确认 %d", id, cum, sub.ackedBytes)
	}
	newly := cum - sub.ackedBytes
	if newly > sub.inflightBytes {
		return reject(ErrAckOutOfRange, "子流 %q 确认 %d 字节超出在途 %d 字节", id, newly, sub.inflightBytes)
	}
	// 对齐检查：确认字节数必须恰好落在在途段边界上（先校验后应用，
	// 被拒绝的事件不得改变任何状态）。
	rem := newly
	for i := 0; i < sub.inflight.len() && rem > 0; i++ {
		g := sub.inflight.at(i)
		if g.len > rem {
			return reject(ErrAckMisaligned,
				"子流 %q 确认 %d 字节未落在段边界上（段 [%d,%d) 被部分确认）",
				id, newly, g.seq, g.seq+g.len)
		}
		rem -= g.len
	}
	for rem = newly; rem > 0; {
		g, _ := sub.inflight.front()
		sub.inflight.popFront()
		s.stats.InflightPops++
		s.stats.SegmentsReleased++
		rem -= g.len
	}
	sub.inflightBytes -= newly
	sub.ackedBytes = cum
	return s.finish(), nil
}

// ConnAck 连接级确认：累计确认到 ackSeq（不含），并通告新的接收窗口。
// 确认序号不得倒退，否则视为过期事件，报错且不改变任何状态。
func (s *Scheduler) ConnAck(ackSeq int64, window int64) (*Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ackSeq < 0 {
		return reject(ErrInvalidArgument, "确认序号不能为负，得到 %d", ackSeq)
	}
	if window < 0 {
		return reject(ErrInvalidArgument, "通告窗口不能为负，得到 %d", window)
	}
	if ackSeq < s.acked {
		return reject(ErrStaleAck, "确认序号 %d 小于已确认序号 %d", ackSeq, s.acked)
	}
	if ackSeq > s.sentMax {
		return reject(ErrAckOutOfRange, "确认序号 %d 超出已发送范围 %d", ackSeq, s.sentMax)
	}
	s.acked = ackSeq
	s.window = window
	// 丢弃已被连接级确认覆盖的待发重新注入段。
	for {
		rs, ok := s.reinj.min()
		if !ok || rs.seq+rs.len > s.acked {
			break
		}
		s.reinj.popMin()
		s.stats.TreapRemovals++
		s.stats.SegmentsDropped++
	}
	return s.finish(), nil
}

// SubflowFail 标记子流失效：在途段全部从其在途中清除，其中尚未被
// 连接级确认覆盖的段作为重新注入段排到待发队列最前（按序号升序，
// 优先于新数据），且不得再选择该失效子流。
func (s *Scheduler) SubflowFail(id string) (*Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return reject(ErrInvalidArgument, "子流标识不能为空")
	}
	sub, ok := s.subs[id]
	if !ok {
		return reject(ErrSubflowNotFound, "子流 %q 不存在", id)
	}
	if !sub.active {
		// 已失效：幂等空操作。
		return s.finish(), nil
	}
	sub.active = false
	if sub.role == Normal {
		s.activeNormal--
	}
	n := sub.inflight.len()
	for i := 0; i < n; i++ {
		g := sub.inflight.at(i)
		s.stats.SegmentsEvicted++
		s.stats.InflightPops++
		if g.seq+g.len <= s.acked {
			continue // 已被连接级确认覆盖，无需重新注入
		}
		if s.reinj.contains(g.seq) {
			continue // 防御性去重：同一序号的待发重新注入段只保留一份
		}
		s.reinj.insert(seg{seq: g.seq, len: g.len, excluded: id})
		s.stats.TreapInserts++
	}
	sub.inflight.clear()
	sub.inflightBytes = 0
	return s.finish(), nil
}

// SubflowRecover 恢复子流：拥塞窗口重置为添加时的初始值，在途为零。
func (s *Scheduler) SubflowRecover(id string) (*Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return reject(ErrInvalidArgument, "子流标识不能为空")
	}
	sub, ok := s.subs[id]
	if !ok {
		return reject(ErrSubflowNotFound, "子流 %q 不存在", id)
	}
	if sub.active {
		// 已活跃：幂等空操作。
		return s.finish(), nil
	}
	sub.active = true
	sub.cwnd = sub.initialCwnd
	if sub.role == Normal {
		s.activeNormal++
	}
	return s.finish(), nil
}

// SetRTT 修改子流往返时延估计（毫秒，正整数）。
func (s *Scheduler) SetRTT(id string, rtt int64) (*Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return reject(ErrInvalidArgument, "子流标识不能为空")
	}
	if rtt <= 0 {
		return reject(ErrInvalidArgument, "往返时延必须为正，得到 %d", rtt)
	}
	sub, ok := s.subs[id]
	if !ok {
		return reject(ErrSubflowNotFound, "子流 %q 不存在", id)
	}
	sub.rtt = rtt
	return s.finish(), nil
}

// Snapshot 返回当前状态的只读快照（主要用于测试与校验）。
func (s *Scheduler) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{
		NextSeq:         s.nextSeq,
		SentMax:         s.sentMax,
		Acked:           s.acked,
		Window:          s.window,
		PendingReinject: s.reinj.inorder(),
		PendingFresh:    s.fresh.snapshot(),
	}
	ids := make([]string, 0, len(s.subs))
	for id := range s.subs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		sub := s.subs[id]
		st.Subflows = append(st.Subflows, SubflowState{
			ID:            sub.id,
			RTT:           sub.rtt,
			Cwnd:          sub.cwnd,
			InitialCwnd:   sub.initialCwnd,
			Role:          sub.role,
			Active:        sub.active,
			AckedBytes:    sub.ackedBytes,
			SentBytes:     sub.sentBytes,
			InflightBytes: sub.inflightBytes,
			Inflight:      sub.inflight.snapshot(),
		})
	}
	return st
}

// Stats 返回基本操作计数的快照。
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

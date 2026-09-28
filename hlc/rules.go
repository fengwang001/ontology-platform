package hlc

import "strconv"

// overflowError 表示计数在需要继续递增时已达到 uint64 上限。
type overflowError struct{}

func (overflowError) Error() string { return "counter overflow: C already at math.MaxUint64" }

// tick 按 HLC 规则从当前时钟 cur、本地非负物理读数 phys 推进一次。
// recv 为 true 时 remote 为消息携带的发送时间戳，必须并入比较。
// 返回新时间戳、用于日志的判定依据，以及计数溢出错误。
//
// 规则（严格字典序）：
//
//	local/send: L' = max(cur.L, phys)
//	receive:    L' = max(cur.L, phys, remote.L)
//	C' = cur.C+1                     若 L' == cur.L（未与本地相等的其他来源比较）
//	C' = remote.C+1                  若 receive 且 L' == remote.L 且 remote.L > cur.L
//	C' = 0                           若 L' > 所有已见逻辑时间（物理时钟领先）
//	并列时取较大候选计数再加一。
func tick(cur Timestamp, phys uint64, remote Timestamp, recv bool) (Timestamp, string, error) {
	l := cur.L
	basis := "cur.L"
	if phys > l {
		l = phys
		basis = "phys"
	}
	if recv && remote.L > l {
		l = remote.L
		basis = "remote.L"
	}

	var nextC uint64
	var reason string
	switch {
	case recv && l == remote.L && remote.L >= cur.L:
		// 逻辑时间由消息锚定（或消息与本地并列，取较大计数再加一）。
		baseC := remote.C
		tie := false
		if remote.L == cur.L && cur.C > remote.C {
			baseC = cur.C
			tie = true
		}
		if baseC == ^uint64(0) {
			return Timestamp{}, "", overflowError{}
		}
		nextC = baseC + 1
		reason = "L'=max(cur.L,phys,remote.L)=" + uintToString(l) +
			", C'=max(cur.C,remote.C)+1=" + uintToString(nextC)
		if !tie && remote.L > cur.L {
			reason = "L'=max(cur.L,phys,remote.L)=" + uintToString(l) +
				" (anchor=remote.L), C'=remote.C+1=" + uintToString(nextC)
		}
	case l == cur.L:
		if cur.C == ^uint64(0) {
			return Timestamp{}, "", overflowError{}
		}
		nextC = cur.C + 1
		reason = "L'=max(cur.L,phys)=" + uintToString(l) + " (anchor=" + basis +
			" unchanged), C'=cur.C+1"
	default:
		nextC = 0
		reason = "L'=" + uintToString(l) + " advanced by " + basis + ", C'=0"
	}
	return Timestamp{L: l, C: nextC}, reason, nil
}

func (s *System) validateNode(op, name string) (*nodeState, *Error) {
	if name == "" {
		e := errf(ErrInvalidArgument, op, "", "", "node name is empty")
		s.logRejectErr(e)
		return nil, e
	}
	ns, ok := s.nodes[name]
	if !ok {
		e := errf(ErrNodeNotFound, op, name, "", "node does not exist")
		s.logRejectErr(e)
		return nil, e
	}
	return ns, nil
}

func (s *System) validateNodeExists(op, name string) *Error {
	if name == "" {
		return errf(ErrInvalidArgument, op, "", "", "node name is empty")
	}
	if _, ok := s.nodes[name]; !ok {
		return errf(ErrNodeNotFound, op, name, "", "node does not exist")
	}
	return nil
}

func validatePhysical(op, node string, physical int64) (uint64, *Error) {
	if physical < 0 {
		return 0, errf(ErrNegativePhysical, op, node, "",
			"physical reading %d is negative", physical)
	}
	return uint64(physical), nil
}

// appendEventLocked 分配全局序号并追加历史；调用方必须持有 mu 且已完成全部校验与时钟计算。
func (s *System) appendEventLocked(node string, kind Kind, phys uint64, ts Timestamp,
	msgID, to, from string, related uint64) *Event {
	s.seq++
	ev := &Event{
		Seq:        s.seq,
		Node:       node,
		Kind:       kind,
		Physical:   phys,
		Timestamp:  ts,
		MessageID:  msgID,
		To:         to,
		From:       from,
		RelatedSeq: related,
	}
	s.events = append(s.events, ev)
	return ev
}

func eventLess(a, b *Event) bool {
	if c := a.Timestamp.Compare(b.Timestamp); c != 0 {
		return c < 0
	}
	return a.Seq < b.Seq
}

func uintToString(v uint64) string { return strconv.FormatUint(v, 10) }

func (s *System) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}

func (s *System) logReject(op, node, msgID, detail string, code ErrorCode) {
	s.logf("%s node=%q message=%q REJECTED %s: %s", op, node, msgID, code, detail)
}

func (s *System) logRejectErr(e *Error) {
	s.logf("%s node=%q message=%q REJECTED %s: %s",
		e.Operation, e.Node, e.MessageID, e.Code, e.Detail)
}

func (s *System) logTick(op, node, msgID string, phys, seq uint64, reason string, ts Timestamp) {
	s.logf("%s node=%s message=%q physical=%d -> event#%d ts=%s | %s",
		op, node, msgID, phys, seq, ts, reason)
}

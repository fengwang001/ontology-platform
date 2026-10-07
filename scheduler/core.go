package scheduler

import "fmt"

// finish 在事件被接受后统一执行发送决策并汇总输出。
func (s *Scheduler) finish() *Decision {
	s.stats.Events++
	d := &Decision{}
	s.transmit(d)
	return d
}

// transmit 从待发队列头部依次尝试发送，遇到无法发送的段即停止
// （队首阻塞：后续段不得越过它先发）。
//
// 队列结构为 [重新注入段（treap，按序号升序）] + [新数据段（FIFO）]。
// 重新注入段不受连接级窗口约束，但受选路约束且排除失效来源子流；
// 新数据段同时受连接级窗口与选路约束。
func (s *Scheduler) transmit(d *Decision) {
	for {
		if rs, ok := s.reinj.min(); ok {
			sub := s.pick(rs.len, rs.excluded)
			if sub == nil {
				d.Blocked = fmt.Sprintf("重新注入段 [%d,%d) 等待：无满足条件的子流（排除失效来源 %q）",
					rs.seq, rs.seq+rs.len, rs.excluded)
				return
			}
			s.reinj.popMin()
			s.stats.TreapRemovals++
			s.sendOn(sub, rs, true, d)
			continue
		}
		fs, ok := s.fresh.front()
		if !ok {
			return
		}
		if fs.seq+fs.len > s.acked+s.window {
			d.Blocked = fmt.Sprintf("新数据段 [%d,%d) 超出连接级接收窗口：已确认 %d + 通告窗口 %d",
				fs.seq, fs.seq+fs.len, s.acked, s.window)
			return
		}
		sub := s.pick(fs.len, "")
		if sub == nil {
			d.Blocked = fmt.Sprintf("新数据段 [%d,%d) 等待：无满足条件的子流", fs.seq, fs.seq+fs.len)
			return
		}
		s.fresh.popFront()
		s.stats.FreshPops++
		s.sendOn(sub, fs, false, d)
	}
}

// pick 为长度为 need 的段选择子流：
// 须活跃、拥塞窗口减在途不小于 need、不等于 excluded；
// 存在活跃普通子流时只在普通子流中选，否则才考虑备用子流；
// 候选中取往返时延最小者，并列取标识较小者。
func (s *Scheduler) pick(need int64, excluded string) *subflow {
	s.stats.PickCalls++
	want := Normal
	if s.activeNormal == 0 {
		want = Backup
	}
	var best *subflow
	for _, sub := range s.subs {
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

// sendOn 在选定子流上发出一个段，更新在途与统计。
func (s *Scheduler) sendOn(sub *subflow, g seg, resend bool, d *Decision) {
	sub.inflight.push(seg{seq: g.seq, len: g.len})
	sub.inflightBytes += g.len
	sub.sentBytes += g.len
	s.stats.InflightPushes++
	s.stats.SegmentsSent++
	kind := "新数据"
	if resend {
		s.stats.SegmentsResent++
		kind = "重新注入"
	} else {
		s.sentMax = g.seq + g.len
	}
	d.Sent = append(d.Sent, SentSegment{
		Seq:     g.seq,
		Len:     g.len,
		Subflow: sub.id,
		Resend:  resend,
		Reason: fmt.Sprintf("%s段 [%d,%d)：子流 %q 当选（rtt=%dms，发出后在途 %d/%d 字节）",
			kind, g.seq, g.seq+g.len, sub.id, sub.rtt, sub.inflightBytes, sub.cwnd),
	})
}

package service_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/atp/clock"
	"ontology/atp/order"
	"ontology/atp/reject"
	"ontology/atp/service"
)

// 随机操作序列对照测试：同一随机操作序列同时作用于正式实现与
// 独立编写的朴素模型，逐步比对输出（成功/拒绝类别/缺货行/
// 发货仓分配/查询结果），并在每步后校验不变量。
// 日志（go test -v 可见）打印每步的输入、输出与判定依据。

type step struct {
	kind    string
	now     int64
	wh      string
	sku     string
	id      string
	qty     int64
	arrival int64
	req     order.Request
	at      int64
}

func (s step) String() string {
	switch s.kind {
	case "commit":
		return fmt.Sprintf("commit(%s @%d lines=%v split=%v ttl=%d maxWh=%d)",
			s.req.OrderID, s.now, s.req.Lines, s.req.AllowSplit, s.req.TTL, s.req.MaxWarehouses)
	case "addStock":
		return fmt.Sprintf("addStock(@%d %s %s +%d)", s.now, s.wh, s.sku, s.qty)
	case "inbound":
		return fmt.Sprintf("scheduleInbound(@%d %s %s %s arrival=%d qty=%d)", s.now, s.wh, s.sku, s.id, s.arrival, s.qty)
	case "confirmIn":
		return fmt.Sprintf("confirmInbound(@%d %s %s %s)", s.now, s.wh, s.sku, s.id)
	case "confirmOut":
		return fmt.Sprintf("confirmOutbound(@%d %s)", s.now, s.id)
	case "release":
		return fmt.Sprintf("release(@%d %s)", s.now, s.id)
	case "queryATP":
		return fmt.Sprintf("queryATP(%s %s @%d)", s.wh, s.sku, s.at)
	case "queryOrder":
		return fmt.Sprintf("queryOrder(%s)", s.id)
	}
	return s.kind
}

// outcome 为可比对的操作结果。
type outcome struct {
	reason reject.Reason // -1 表示成功
	line   int
	allocs string // 发货仓分配的规范化文本（判定依据）
	value  int64  // 查询类操作的返回值
	detail string // 订单明细的规范化文本
}

const okReason = reject.Reason(-1)

func outcomeOf(err error) outcome {
	if err == nil {
		return outcome{reason: okReason, line: -1}
	}
	var re *reject.Error
	o := outcome{reason: reject.InvalidParam, line: -1}
	if e, ok := err.(*reject.Error); ok {
		re = e
		o.reason = re.Reason
		o.line = re.Line
	}
	return o
}

func runOnService(s *service.Service, st step) outcome {
	switch st.kind {
	case "addStock":
		return outcomeOf(s.AddStock(clock.Time(st.now), st.wh, st.sku, st.qty))
	case "inbound":
		return outcomeOf(s.ScheduleInbound(clock.Time(st.now), st.wh, st.sku, st.id, clock.Time(st.arrival), st.qty))
	case "confirmIn":
		return outcomeOf(s.ConfirmInbound(clock.Time(st.now), st.wh, st.sku, st.id))
	case "confirmOut":
		return outcomeOf(s.ConfirmOutbound(clock.Time(st.now), st.id))
	case "release":
		return outcomeOf(s.Release(clock.Time(st.now), st.id))
	case "commit":
		res, err := s.Commit(st.req)
		o := outcomeOf(err)
		if err == nil {
			o.allocs = fmt.Sprintf("expiry=%d %v", res.Expiry, res.Allocations)
		}
		return o
	case "queryATP":
		v, err := s.QueryATP(st.wh, st.sku, clock.Time(st.at))
		o := outcomeOf(err)
		o.value = v
		return o
	case "queryOrder":
		d, err := s.QueryOrder(st.id)
		o := outcomeOf(err)
		if err == nil {
			o.detail = fmt.Sprintf("valid=%v expiry=%d lines=%v", d.Valid, d.Expiry, d.Lines)
		}
		return o
	}
	panic("unknown step")
}

func runOnModel(m *naive, st step) outcome {
	switch st.kind {
	case "addStock":
		return outcomeOf(m.addStock(st.now, st.wh, st.sku, st.qty))
	case "inbound":
		return outcomeOf(m.scheduleInbound(st.now, st.wh, st.sku, st.id, st.arrival, st.qty))
	case "confirmIn":
		return outcomeOf(m.confirmInbound(st.now, st.wh, st.sku, st.id))
	case "confirmOut":
		return outcomeOf(m.confirmOutbound(st.now, st.id))
	case "release":
		return outcomeOf(m.release(st.now, st.id))
	case "commit":
		allocs, expiry, err := m.commit(st.req)
		o := outcomeOf(err)
		if err == nil {
			o.allocs = fmt.Sprintf("expiry=%d %s", expiry, fmtAllocs(allocs))
		}
		return o
	case "queryATP":
		v, err := m.queryATP(st.wh, st.sku, st.at)
		o := outcomeOf(err)
		o.value = v
		return o
	case "queryOrder":
		d, err := m.queryOrder(st.id)
		o := outcomeOf(err)
		if err == nil {
			var lines []string
			for _, l := range d.lines {
				lines = append(lines, fmt.Sprintf("{%s %s %d}", l.wh, l.sku, l.qty))
			}
			o.detail = fmt.Sprintf("valid=%v expiry=%d lines=[%s]", d.valid, d.expiry, joinLines(lines))
		}
		return o
	}
	panic("unknown step")
}

func joinLines(lines []string) string {
	return strings.Join(lines, " ")
}

func fmtAllocs(allocs []nAlloc) string {
	out := "["
	for i, a := range allocs {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("{%d %s %s %d}", a.line, a.wh, a.sku, a.qty)
	}
	return out + "]"
}

func genSteps(r *rand.Rand, n int) []step {
	skus := []string{"skuA", "skuB", "skuC"}
	whs := []string{"w1", "w2", "w3"}
	var steps []step
	var now int64
	var orderSeq, inboundSeq int
	var orderIDs []string
	var inboundIDs []string

	advance := func() int64 {
		if r.Intn(100) < 6 {
			// 偶尔回退时钟（含负值）以测试时钟回退/参数非法。
			return now - int64(r.Intn(8)+1)
		}
		now += int64(r.Intn(5))
		return now
	}
	pickOrder := func() string {
		if len(orderIDs) > 0 && r.Intn(100) < 40 {
			return orderIDs[r.Intn(len(orderIDs))]
		}
		id := fmt.Sprintf("ord-%d", orderSeq)
		orderSeq++
		orderIDs = append(orderIDs, id)
		return id
	}

	for i := 0; i < n; i++ {
		switch r.Intn(10) {
		case 0, 1: // 补现货
			steps = append(steps, step{kind: "addStock", now: advance(),
				wh: whs[r.Intn(3)], sku: skus[r.Intn(3)], qty: int64(r.Intn(20) + 1)})
		case 2: // 计划入库
			id := fmt.Sprintf("in-%d", inboundSeq)
			inboundSeq++
			inboundIDs = append(inboundIDs, id)
			t := advance()
			steps = append(steps, step{kind: "inbound", now: t,
				wh: whs[r.Intn(3)], sku: skus[r.Intn(3)], id: id,
				arrival: t + int64(r.Intn(40)), qty: int64(r.Intn(15) + 1)})
		case 3: // 确认到货（可能不存在/已确认/未到期）
			id := fmt.Sprintf("in-%d", r.Intn(inboundSeq+2))
			steps = append(steps, step{kind: "confirmIn", now: advance(),
				wh: whs[r.Intn(3)], sku: skus[r.Intn(3)], id: id})
		case 4, 5, 6: // 订单承诺
			nLines := r.Intn(3) + 1
			var lines []order.Line
			for j := 0; j < nLines; j++ {
				qty := int64(r.Intn(15) + 1)
				if r.Intn(100) < 4 {
					qty = 0 // 偶尔非法参数
				}
				lines = append(lines, order.Line{SKU: skus[r.Intn(3)], Qty: qty})
			}
			maxWh := r.Intn(3) + 1
			if r.Intn(100) < 4 {
				maxWh = 0 // 偶尔非法参数
			}
			t := advance()
			steps = append(steps, step{kind: "commit", now: t, req: order.Request{
				OrderID:       pickOrder(),
				Lines:         lines,
				Now:           clock.Time(t),
				AllowSplit:    r.Intn(2) == 0,
				TTL:           int64(r.Intn(60)),
				MaxWarehouses: maxWh,
			}})
		case 7: // 确认出库
			steps = append(steps, step{kind: "confirmOut", now: advance(), id: pickOrder()})
		case 8: // 释放预留
			steps = append(steps, step{kind: "release", now: advance(), id: pickOrder()})
		default: // 查询
			if r.Intn(2) == 0 {
				at := now + int64(r.Intn(50))
				if r.Intn(100) < 8 {
					at = now - int64(r.Intn(5)+1) // 查询时刻早于当前时刻
				}
				steps = append(steps, step{kind: "queryATP",
					wh: whs[r.Intn(3)], sku: skus[r.Intn(3)], at: at})
			} else {
				steps = append(steps, step{kind: "queryOrder", id: pickOrder()})
			}
		}
	}
	return steps
}

func TestRandomizedDifferential(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			svc := service.New()
			mdl := newNaive()
			for i, w := range []struct {
				id   string
				prio int
			}{{"w1", 1}, {"w2", 2}, {"w3", 3}} {
				must(t, svc.AddWarehouse(0, w.id, w.prio))
				must(t, mdl.addWarehouse(0, w.id, w.prio))
				_ = i
			}
			steps := genSteps(r, 300)
			for i, st := range steps {
				gotSvc := runOnService(svc, st)
				gotMdl := runOnModel(mdl, st)
				if gotSvc != gotMdl {
					t.Fatalf("step %d %s:\n service=%+v\n model  =%+v", i, st, gotSvc, gotMdl)
				}
				t.Logf("step %03d in=%s out=%s", i, st, formatOutcome(gotSvc))
				if err := svc.CheckInvariants(); err != nil {
					t.Fatalf("step %d %s: invariant violated: %v", i, st, err)
				}
			}
			examined, sweeps := svc.Stats()
			t.Logf("done: %d steps, examined=%d sweeps=%d", len(steps), examined, sweeps)
		})
	}
}

func formatOutcome(o outcome) string {
	if o.reason == okReason {
		if o.allocs != "" {
			return "ok allocs=" + o.allocs
		}
		if o.detail != "" {
			return "ok " + o.detail
		}
		return fmt.Sprintf("ok value=%d", o.value)
	}
	if o.line >= 0 {
		return fmt.Sprintf("reject=%s line=%d", o.reason, o.line)
	}
	return "reject=" + o.reason.String()
}

package sla

import (
	"math/rand"
	"strings"
)

// 朴素对照模型：保存完整操作日志；每次执行后从空状态开始逐笔全量重放，
// 天气延展对每笔订单扫描全部天气事件（故意 O(W)），
// 不使用任何索引/推送/增量，与生产实现独立。

type nOp struct {
	kind string // accept dispatch ready pickup deliver cancel readdress weather claim auto
	id   string
	at   int64
	wid  string
	wl   int64
	wr   int64
	wext int64
}

type nOrder struct {
	id, cfgKey             string
	acceptedAt, promisedAt int64
	dispatchAt, readyAt    int64
	pickupAt, deliverAt    int64
	hasDispatch, hasReady  bool
	hasPickup, hasDeliver  bool
	canceled, readdressed  bool
	readdressAt            int64
}

type Naive struct {
	ops     []nOp
	cfgs    map[string]Config
	weather map[string]bool
}

func newNaive() *Naive {
	return &Naive{cfgs: map[string]Config{"": testConfig()}, weather: map[string]bool{}}
}

// Step 尝试执行一条操作；返回 (错误码, 裁决或nil, 是否改变日志)。
// 重放时每个时刻都从日志全量重建状态。
func (n *Naive) step(op nOp) (Code, *LedgerEntry) {
	code, entry := n.replay(append(append([]nOp{}, n.ops...), op))
	if code == CodeOK {
		n.ops = append(n.ops, op)
	}
	return code, entry
}

// replay 重放给定日志；返回最后一条操作的结果。
// 采用与生产相同的拒绝即不留痕语义：校验失败的操作直接跳过。
func (n *Naive) replay(ops []nOp) (Code, *LedgerEntry) {
	cfg := n.cfgs[""]
	orders := map[string]*nOrder{}
	wReg := map[string][3]int64{} // id -> l,r,ext（按登记次序）
	var wOrder []string
	ledger := map[string]LedgerEntry{}
	var lastAccept int64

	var lastCode Code = CodeOK
	var lastEntry *LedgerEntry

	for _, op := range ops {
		lastCode, lastEntry = CodeOK, nil
		switch op.kind {
		case "accept":
			if op.at < lastAccept {
				lastCode = CodeClockRollback
				continue
			}
			if orders[op.id] != nil {
				lastCode = CodeInvalidParam
				continue
			}
			orders[op.id] = &nOrder{
				id: op.id, cfgKey: "", acceptedAt: op.at,
				promisedAt: op.at + cfg.PromiseDuration,
			}
			lastAccept = op.at
		case "dispatch", "ready", "pickup", "deliver":
			o := orders[op.id]
			if o == nil {
				lastCode = CodeOrderNotFound
				continue
			}
			if o.canceled {
				lastCode = CodeOrderCanceled
				continue
			}
			expect := map[string]bool{
				"dispatch": true, "ready": o.hasDispatch,
				"pickup": o.hasReady, "deliver": o.hasPickup,
			}
			done := map[string]bool{
				"dispatch": o.hasDispatch, "ready": o.hasReady,
				"pickup": o.hasPickup, "deliver": o.hasDeliver,
			}
			if !expect[op.kind] || done[op.kind] {
				lastCode = CodeEventOrder
				continue
			}
			switch op.kind {
			case "dispatch":
				o.hasDispatch, o.dispatchAt = true, op.at
			case "ready":
				o.hasReady, o.readyAt = true, op.at
			case "pickup":
				o.hasPickup, o.pickupAt = true, op.at
			case "deliver":
				o.hasDeliver, o.deliverAt = true, op.at
			}
		case "cancel":
			o := orders[op.id]
			if o == nil {
				lastCode = CodeOrderNotFound
				continue
			}
			if o.canceled {
				lastCode = CodeOrderCanceled
				continue
			}
			if o.hasDeliver {
				lastCode = CodeEventOrder
				continue
			}
			o.canceled = true
		case "readdress":
			o := orders[op.id]
			if o == nil {
				lastCode = CodeOrderNotFound
				continue
			}
			if o.canceled {
				lastCode = CodeOrderCanceled
				continue
			}
			if o.hasDeliver {
				lastCode = CodeEventOrder
				continue
			}
			if o.readdressed {
				lastCode = CodeAlreadyReaddressed
				continue
			}
			o.readdressed, o.readdressAt = true, op.at
		case "weather":
			if _, dup := wReg[op.wid]; dup {
				lastCode = CodeInvalidParam
				continue
			}
			wReg[op.wid] = [3]int64{op.wl, op.wr, op.wext}
			wOrder = append(wOrder, op.wid)
		case "claim", "auto":
			o := orders[op.id]
			if o == nil {
				lastCode = CodeOrderNotFound
				continue
			}
			if o.canceled {
				lastCode = CodeOrderCanceled
				continue
			}
			if _, paid := ledger[op.id]; paid {
				lastCode = CodeAlreadyPaid
				continue
			}
			if !o.hasDeliver {
				lastCode = CodeNotDelivered
				continue
			}
			deadline := o.deliverAt + cfg.ClaimWindow
			if op.kind == "claim" {
				if op.at < o.deliverAt || op.at >= deadline {
					lastCode = CodeWindowClosed
					continue
				}
			} else {
				if op.at < deadline {
					lastCode = CodeWindowClosed
					continue
				}
			}
			// 全量扫描：该订单送达前已登记、且区间覆盖原始承诺时刻的天气。
			deliverPos := -1
			for i := range ops {
				if ops[i].kind == "deliver" && ops[i].id == op.id {
					deliverPos = i
				}
			}
			wExt := int64(0)
			for i := 0; i < deliverPos; i++ {
				w := ops[i]
				if w.kind == "weather" &&
					o.promisedAt >= w.wl && o.promisedAt < w.wr {
					wExt += w.wext
				}
			}
			userExt := int64(0)
			if o.readdressed {
				userExt = cfg.UserAddrExtend
			}
			ext := userExt + wExt
			if ext > cfg.ExtendCap {
				ext = cfg.ExtendCap
			}
			extended := o.promisedAt + ext
			delay := o.deliverAt - extended
			if op.kind == "claim" && delay <= 0 {
				lastCode = CodeNoDelay
				continue
			}
			if op.kind == "auto" && delay < cfg.TierThresholds[len(cfg.TierThresholds)-1] {
				lastCode = CodeInvalidParam
				continue
			}
			entry := nAdjudicate(cfg, o, extended, delay, op.at, op.kind == "auto")
			ledger[op.id] = entry
			e := entry
			lastEntry = &e
		}
	}
	return lastCode, lastEntry
}

func nAdjudicate(cfg Config, o *nOrder, extended, delay, at int64, automatic bool) LedgerEntry {
	r := attributionResult{}
	if v := o.readyAt - (o.acceptedAt + cfg.MerchantPrep); v > 0 {
		r.merchant = v
	}
	if v := o.dispatchAt - (o.acceptedAt + cfg.PlatformDispatch); v > 0 {
		r.platform = v
	}
	if v := o.pickupAt - (o.readyAt + cfg.RiderPickup); v > 0 {
		r.rider += v
	}
	rem := extended - o.pickupAt
	if rem < 0 {
		rem = 0
	}
	if v := o.deliverAt - (o.pickupAt + rem); v > 0 {
		r.rider += v
	}
	if o.readdressed && o.readdressAt > o.pickupAt {
		r.user = cfg.UserAddrExtend
	}
	party := r.chooseParty()
	amount := int64(0)
	if party != PartyUser {
		idx := -1
		for i, th := range cfg.TierThresholds {
			if delay >= th {
				idx = i
			}
		}
		if idx >= 0 {
			amount = cfg.TierPayouts[idx]
		}
	}
	return LedgerEntry{OrderID: o.id, At: at, Amount: amount, Delay: delay,
		Party: party, Automatic: automatic}
}

var _ = strings.TrimSpace
var _ = rand.Int

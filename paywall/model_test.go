package paywall_test

// naive 是“每次全量重算”的参考模型：
// 任何状态问题都通过重放此前全部已接受操作得出，登录合并每次都从
// 设备原始匿名账与用户账重新做并集，不信任增量结构。

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ontology/paywall"
)

type nEntry struct {
	at   int64
	gift bool
}

type nToken struct {
	article   string
	issuedAt  int64
	redeemers map[string]struct{}
}

type naive struct {
	n, m, g, k, e int64

	articles map[string]bool
	subs     map[string]int64
	giftCap  map[string]struct {
		month int64
		count int64
	}
	tokens map[int64]nToken
	nextID int64

	// 已接受操作日志（被拒绝的不记录）。
	log []nOp
}

type nOp struct {
	kind    string
	now     int64
	device  string
	user    string
	article string
	token   int64 // 0 = 无
	until   int64
	free    bool
}

func newNaive(cfg paywall.Config) *naive {
	return &naive{
		n: cfg.N, m: cfg.M, g: cfg.G, k: cfg.K, e: cfg.E,
		articles: map[string]bool{},
		subs:     map[string]int64{},
		giftCap: map[string]struct {
			month int64
			count int64
		}{},
		tokens: map[int64]nToken{},
	}
}

// state 是重放到最后一条已接受操作后的完整状态快照。
type state struct {
	bound map[string]string
	// books[subject][month][article] = entry
	books map[string]map[int64]map[string]nEntry
	// 截至当前重放点已知的订阅（按操作时点演进，不使用最新订阅回溯）。
	subs map[string]int64
}

func (nv *naive) replay() *state { return nv.replayN(len(nv.log)) }

// replayN 只重放前 n 条已接受操作。
func (nv *naive) replayN(n int) *state {
	st := &state{
		bound: map[string]string{},
		books: map[string]map[int64]map[string]nEntry{},
		subs:  map[string]int64{},
	}
	book := func(subject string, month int64) map[string]nEntry {
		mon, ok := st.books[subject]
		if !ok {
			mon = map[int64]map[string]nEntry{}
			st.books[subject] = mon
		}
		bk, ok := mon[month]
		if !ok {
			bk = map[string]nEntry{}
			mon[month] = bk
		}
		return bk
	}
	history := nv.log
	if n < len(history) {
		history = history[:n]
	}
	for _, op := range history {
		month := op.now / nv.m
		switch op.kind {
		case "subscribe":
			// 订阅状态也必须按操作时点重放，不能用最新订阅回溯历史 Read。
			st.subs[op.user] = op.until
			if _, ok := st.books[op.user]; !ok {
				st.books[op.user] = map[int64]map[string]nEntry{}
			}
		case "login":
			if _, ok := st.bound[op.device]; ok {
				continue
			}
			// 合并必须基于此刻最新的用户账；早期登录可能已把用户账替换为新 map。
			nv.mergeFresh(st, op.user, op.device, op.now)
			st.bound[op.device] = op.user
		case "logout":
			delete(st.bound, op.device)
		case "read":
			subject := op.device
			if u, ok := st.bound[op.device]; ok {
				subject = u
			}
			bk := book(subject, month)
			free := nv.articles[op.article]
			subscribed := false
			if _, isUser := st.bound[op.device]; isUser {
				if until, ok := st.subs[subject]; ok && op.now < until {
					subscribed = true
				}
			}
			_, unlocked := bk[op.article]
			giftOK := false
			if op.token > 0 {
				if tok, ok := nv.tokens[op.token]; ok && tok.article == op.article &&
					op.now >= tok.issuedAt && op.now < tok.issuedAt+nv.e {
					_, already := tok.redeemers[subject]
					if already || int64(len(tok.redeemers)) < nv.k {
						giftOK = true
					}
				}
			}
			switch {
			case subscribed || free:
				// 不写账
			case unlocked:
				// 已解锁
			case giftOK:
				tok := nv.tokens[op.token]
				tok.redeemers[subject] = struct{}{}
				nv.tokens[op.token] = tok
				bk[op.article] = nEntry{at: op.now, gift: true}
			default:
				used := 0
				for _, en := range bk {
					if !en.gift {
						used++
					}
				}
				if int64(used) < nv.n {
					bk[op.article] = nEntry{at: op.now, gift: false}
				}
			}
		}
	}
	return st
}

// mergeFresh 全量重算并集：礼赠全保留，额度按 (at, article) 取最早 n 篇。
// 每次重放都会遍历全部历史登录，因此输入必须是当前最新的用户账
// （早期登录可能已把 st.books[user][month] 替换成新 map）。
func (nv *naive) mergeFresh(st *state, user, device string, now int64) {
	month := now / nv.m
	get := func(subj string) map[string]nEntry {
		// 不缓存 map 引用：仅在需要读取的瞬间按 subject/month 取最新值。
		if mon, ok := st.books[subj]; ok {
			return mon[month]
		}
		return nil
	}
	union := map[string]nEntry{}
	add := func(e nEntry, art string) {
		cur, ok := union[art]
		switch {
		case !ok:
			union[art] = e
		case e.gift || cur.gift:
			if e.at < cur.at {
				cur.at = e.at
			}
			cur.gift = true
			union[art] = cur
		case e.at < cur.at:
			cur.at = e.at
			union[art] = cur
		}
	}
	for art, e := range get(user) {
		add(e, art)
	}
	for art, e := range get(device) {
		add(e, art)
	}
	type qe struct {
		art string
		at  int64
	}
	var quota []qe
	for art, e := range union {
		if !e.gift {
			quota = append(quota, qe{art, e.at})
		}
	}
	sort.Slice(quota, func(i, j int) bool {
		if quota[i].at != quota[j].at {
			return quota[i].at < quota[j].at
		}
		return quota[i].art < quota[j].art
	})
	keep := map[string]bool{}
	for i := 0; i < len(quota) && int64(i) < nv.n; i++ {
		keep[quota[i].art] = true
	}
	for art, e := range union {
		if !e.gift && !keep[art] {
			delete(union, art)
		}
	}
	if _, ok := st.books[user]; !ok {
		st.books[user] = map[int64]map[string]nEntry{}
	}
	st.books[user][month] = union
}

func (nv *naive) lastAccepted() int64 {
	if len(nv.log) == 0 {
		return -1
	}
	return nv.log[len(nv.log)-1].now
}

// apply 重放全部历史以判定当前操作，返回 (allowed, reason, err)。
// reason: -1 blocked, 0..4 对应 paywall.Reason*。
func (nv *naive) apply(op nOp) (bool, int, error) {
	invalid := func(b bool) error {
		if b {
			return paywall.ErrInvalidArgument
		}
		return nil
	}
	rollback := op.now < nv.lastAccepted()
	switch op.kind {
	case "addArticle":
		if e := invalid(len(op.article) == 0); e != nil {
			return false, 0, e
		}
		if ex, ok := nv.articles[op.article]; ok && ex != op.free {
			return false, 0, paywall.ErrInvalidArgument
		}
		nv.articles[op.article] = op.free
		nv.log = append(nv.log, op)
		return false, 0, nil
	case "subscribe":
		if e := invalid(len(op.user) == 0 || op.now < 0 || op.now > 1e12 || op.until <= op.now); e != nil {
			return false, 0, e
		}
		if rollback {
			return false, 0, paywall.ErrClockRollback
		}
		nv.subs[op.user] = op.until
		nv.log = append(nv.log, op)
		return false, 0, nil
	case "gift":
		if e := invalid(len(op.user) == 0 || len(op.article) == 0 || op.now < 0 || op.now > 1e12); e != nil {
			return false, 0, e
		}
		if rollback {
			return false, 0, paywall.ErrClockRollback
		}
		if _, ok := nv.articles[op.article]; !ok {
			return false, 0, paywall.ErrArticleUnknown
		}
		until, ok := nv.subs[op.user]
		if !ok || op.now >= until {
			return false, 0, paywall.ErrNotSubscribed
		}
		mc := nv.giftCap[op.user]
		month := op.now / nv.m
		if mc.month != month {
			mc.month, mc.count = month, 0
		}
		if mc.count >= nv.g {
			return false, 0, paywall.ErrGiftExhausted
		}
		mc.count++
		nv.giftCap[op.user] = mc
		nv.nextID++
		nv.tokens[nv.nextID] = nToken{article: op.article, issuedAt: op.now, redeemers: map[string]struct{}{}}
		nv.log = append(nv.log, op)
		return false, 0, nil
	case "login":
		if e := invalid(len(op.device) == 0 || len(op.user) == 0 || op.now < 0 || op.now > 1e12); e != nil {
			return false, 0, e
		}
		if rollback {
			return false, 0, paywall.ErrClockRollback
		}
		st := nv.replay()
		if _, ok := st.bound[op.device]; ok {
			return false, 0, paywall.ErrAlreadyBound
		}
		nv.log = append(nv.log, op)
		return false, 0, nil
	case "logout":
		if e := invalid(len(op.device) == 0 || op.now < 0 || op.now > 1e12); e != nil {
			return false, 0, e
		}
		if rollback {
			return false, 0, paywall.ErrClockRollback
		}
		st := nv.replay()
		if _, ok := st.bound[op.device]; !ok {
			return false, 0, paywall.ErrNotBound
		}
		nv.log = append(nv.log, op)
		return false, 0, nil
	case "read":
		if e := invalid(len(op.device) == 0 || len(op.article) == 0 || op.now < 0 || op.now > 1e12); e != nil {
			return false, 0, e
		}
		if rollback {
			return false, 0, paywall.ErrClockRollback
		}
		if _, ok := nv.articles[op.article]; !ok {
			return false, 0, paywall.ErrArticleUnknown
		}
		// 先基于“此前”已接受操作全量重放判定，再把本次操作入账（拦截也入账推进时钟）。
		st := nv.replay()
		subject := op.device
		bound := false
		if u, ok := st.bound[op.device]; ok {
			subject, bound = u, true
		}
		month := op.now / nv.m
		bk := map[string]nEntry{}
		if mon, ok := st.books[subject]; ok {
			bk = mon[month]
		}
		if bound {
			if until, ok := st.subs[subject]; ok && op.now < until {
				nv.log = append(nv.log, op)
				return true, int(paywall.ReasonSubscription), nil
			}
		}
		if nv.articles[op.article] {
			nv.log = append(nv.log, op)
			return true, int(paywall.ReasonFree), nil
		}
		if _, ok := bk[op.article]; ok {
			nv.log = append(nv.log, op)
			return true, int(paywall.ReasonUnlocked), nil
		}
		if op.token > 0 {
			if tok, ok := nv.tokens[op.token]; ok && tok.article == op.article &&
				op.now >= tok.issuedAt && op.now < tok.issuedAt+nv.e {
				if _, already := tok.redeemers[subject]; already || int64(len(tok.redeemers)) < nv.k {
					tok.redeemers[subject] = struct{}{}
					nv.tokens[op.token] = tok
					nv.log = append(nv.log, op)
					return true, int(paywall.ReasonGift), nil
				}
			}
		}
		used := 0
		for _, en := range bk {
			if !en.gift {
				used++
			}
		}
		nv.log = append(nv.log, op)
		if int64(used) < nv.n {
			return true, int(paywall.ReasonQuota), nil
		}
		return false, int(paywall.ReasonBlocked), nil
	}
	return false, 0, fmt.Errorf("unknown op %s", op.kind)
}

func sameError(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b) || errors.Is(b, a) ||
		strings.Contains(a.Error(), b.Error()) && strings.Contains(b.Error(), a.Error())
}

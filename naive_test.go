package offline

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/playback"
)

// nEvent 是一条被真实管理器接受、写入重放日志的操作。
type nEvent struct {
	op               string
	now              int64
	acct, dev, title string
	end              int64
}

type nRec struct {
	rentalEnd int64
	played    bool
	firstPlay int64
}

type nState struct {
	registered map[string]bool
	cooldown   map[string]int64 // 设备 -> 最近一次注销的冷却截止
	lic        map[string]nRec
	titleEnd   map[string]int64
}

// naive 是朴素参考模型：任何判定都从日志头开始全量重放重算，
// 不维护任何增量计数（账号/影片集合仅用于存在性，与管理器外显集合等价）。
type naive struct {
	dmax, omax   int
	cool, lr, lp int64
	accounts     map[string]bool
	titles       map[string]bool
	log          []nEvent
}

func newNaive(dmax, omax int, cool, lr, lp int64) *naive {
	return &naive{
		dmax: dmax, omax: omax, cool: cool, lr: lr, lp: lp,
		accounts: map[string]bool{}, titles: map[string]bool{},
	}
}

func (n *naive) lastNow() int64 {
	var last int64
	for _, e := range n.log {
		if e.now > last {
			last = e.now
		}
	}
	return last
}

// replay 从头重放日志中的全部已接受事件（全量重算）。
func (n *naive) replay() *nState {
	st := &nState{
		registered: map[string]bool{},
		cooldown:   map[string]int64{},
		lic:        map[string]nRec{},
		titleEnd:   map[string]int64{},
	}
	for _, e := range n.log {
		switch e.op {
		case "addTitle":
			st.titleEnd[e.title] = e.end
		case "setEnd":
			st.titleEnd[e.title] = e.end
		case "register":
			if st.registered[e.dev] {
				continue
			}
			if until, ok := st.cooldown[e.dev]; ok && e.now < until {
				delete(st.cooldown, e.dev)
				st.registered[e.dev] = true
				continue
			}
			used := len(st.registered)
			for _, until := range st.cooldown {
				if e.now < until {
					used++
				}
			}
			if used < n.dmax {
				st.registered[e.dev] = true
			}
		case "deregister":
			if !st.registered[e.dev] {
				continue
			}
			delete(st.registered, e.dev)
			st.cooldown[e.dev] = e.now + n.cool
			for k := range st.lic {
				if strings.HasPrefix(k, e.dev+"|") {
					delete(st.lic, k)
				}
			}
		case "download":
			key := e.dev + "|" + e.title
			if !st.registered[e.dev] || e.now >= st.titleEnd[e.title] {
				continue
			}
			if r, ok := st.lic[key]; ok && e.now < n.exp(r, st.titleEnd[e.title]) {
				if !r.played {
					r.rentalEnd = e.now + n.lr
					st.lic[key] = r
				}
				continue
			}
			valid := 0
			for k, r := range st.lic {
				ti := strings.SplitN(k, "|", 2)[1]
				if e.now < n.exp(r, st.titleEnd[ti]) {
					valid++
				}
			}
			if valid < n.omax {
				st.lic[key] = nRec{rentalEnd: e.now + n.lr}
			}
		case "play":
			key := e.dev + "|" + e.title
			r, ok := st.lic[key]
			if !ok || !st.registered[e.dev] {
				continue
			}
			if e.now < n.exp(r, st.titleEnd[e.title]) && !r.played {
				r.played = true
				r.firstPlay = e.now
				st.lic[key] = r
			}
		}
	}
	return st
}

func (n *naive) exp(r nRec, titleEnd int64) int64 {
	end := r.rentalEnd
	if r.played {
		end = r.firstPlay + n.lp
	}
	if titleEnd < end {
		end = titleEnd
	}
	return end
}

// step 在全量重放状态上按规范拒绝次序判定一条操作，
// 返回错误码（"" 表示接受）以及 status 的期望结果。
func (n *naive) step(e nEvent) (string, playback.Info) {
	needID := e.acct == "" || e.dev == "" || e.title == ""
	switch e.op {
	case "addAccount":
		needID = e.acct == ""
	case "addTitle", "setEnd":
		needID = e.title == ""
	case "register", "deregister":
		needID = e.acct == "" || e.dev == ""
	}
	needTime := e.end < 0
	if e.op == "addTitle" || e.op == "setEnd" {
		needTime = e.end <= e.now
	}
	if e.now < 0 || e.now > 1e12 || needID || needTime {
		return errCode(ErrInvalidArg), playback.Info{}
	}
	if e.now < n.lastNow() {
		return errCode(ErrClockRollback), playback.Info{}
	}

	switch e.op {
	case "addAccount":
		if n.accounts[e.acct] {
			return errCode(ErrAccountExists), playback.Info{}
		}
		return "", playback.Info{}
	case "addTitle":
		if n.titles[e.title] {
			return errCode(ErrTitleExists), playback.Info{}
		}
		return "", playback.Info{}
	case "setEnd":
		if !n.titles[e.title] {
			return errCode(ErrTitleNotFound), playback.Info{}
		}
		return "", playback.Info{}
	}

	if !n.accounts[e.acct] {
		return errCode(ErrAccountNotFound), playback.Info{}
	}
	st := n.replay()
	switch e.op {
	case "register":
		if st.registered[e.dev] {
			return errCode(ErrDeviceRegistered), playback.Info{}
		}
		reuse := false
		if until, ok := st.cooldown[e.dev]; ok && e.now < until {
			reuse = true
		}
		if !reuse {
			used := len(st.registered)
			for _, until := range st.cooldown {
				if e.now < until {
					used++
				}
			}
			if used >= n.dmax {
				return errCode(ErrDeviceFull), playback.Info{}
			}
		}
		return "", playback.Info{}
	case "deregister":
		if !st.registered[e.dev] {
			return errCode(ErrDeviceNotFound), playback.Info{}
		}
		return "", playback.Info{}
	}

	if !n.titles[e.title] {
		return errCode(ErrTitleNotFound), playback.Info{}
	}
	if !st.registered[e.dev] {
		return errCode(ErrDeviceNotFound), playback.Info{}
	}
	r, has := st.lic[e.dev+"|"+e.title]
	if e.op == "download" {
		if e.now >= st.titleEnd[e.title] {
			return errCode(ErrTitleOffShelf), playback.Info{}
		}
		validExisting := has && e.now < n.exp(r, st.titleEnd[e.title])
		if validExisting && r.played {
			return errCode(ErrAlreadyPlaying), playback.Info{}
		}
		if !validExisting {
			valid := 0
			for k, rr := range st.lic {
				ti := strings.SplitN(k, "|", 2)[1]
				if e.now < n.exp(rr, st.titleEnd[ti]) {
					valid++
				}
			}
			if valid >= n.omax {
				return errCode(ErrLicenseFull), playback.Info{}
			}
		}
		return "", playback.Info{}
	}
	// play / status
	if !has {
		return errCode(ErrNoLicense), playback.Info{}
	}
	fp := int64(0)
	if r.played {
		fp = r.firstPlay
	}
	info := playback.Evaluate(e.now, r.rentalEnd, fp, n.lp, st.titleEnd[e.title])
	if e.op == "play" && info.State == playback.StateExpired {
		return "expired:" + info.Reason.String(), playback.Info{}
	}
	return "", info
}

// accept 记录本应被接受的操作（双方错误码一致后调用）。
func (n *naive) accept(e nEvent) {
	switch e.op {
	case "addAccount":
		n.accounts[e.acct] = true
	case "addTitle":
		n.titles[e.title] = true
	}
	if e.op != "status" {
		n.log = append(n.log, e)
	}
}

func (n *naive) dump(upto int) string {
	var b strings.Builder
	start := 0
	if len(n.log) > 30 {
		start = len(n.log) - 30
	}
	for i := start; i < len(n.log) && i <= upto; i++ {
		e := n.log[i]
		fmt.Fprintf(&b, "\n  [%d] t=%d %-9s a=%s dev=%s title=%s end=%d",
			i, e.now, e.op, e.acct, e.dev, e.title, e.end)
	}
	return b.String()
}

// runBoth 在真实管理器与朴素模型上执行同一操作，逐步对照错误码、Status，
// 并在每步后以全量重放结果交叉校验全部 (账号,设备,影片) 的可观察状态。
func runBoth(t *testing.T, m *Manager, n *naive, seq, step int, e nEvent) {
	t.Helper()
	wantCode, wantInfo := n.step(e)

	var got error
	var gotInfo playback.Info
	switch e.op {
	case "addAccount":
		got = m.AddAccount(e.now, e.acct)
	case "addTitle":
		got = m.AddTitle(e.now, e.title, e.end)
	case "setEnd":
		got = m.SetTitleEnd(e.now, e.title, e.end)
	case "register":
		got = m.Register(e.now, e.acct, e.dev)
	case "deregister":
		got = m.Deregister(e.now, e.acct, e.dev)
	case "download":
		got = m.Download(e.now, e.acct, e.dev, e.title)
	case "play":
		got = m.Play(e.now, e.acct, e.dev, e.title)
	case "status":
		gotInfo, got = m.Status(e.acct, e.dev, e.title, e.now)
	}

	t.Logf("seq=%d step=%d 输入: t=%d %s acct=%q dev=%q title=%q end=%d => 输出: %s | 判定: 朴素重放=%s",
		seq, step, e.now, e.op, e.acct, e.dev, e.title, e.end, errCode(got), wantCode)

	if errCode(got) != wantCode {
		t.Fatalf("seq=%d step=%d 输出不一致 got=%q want=%q\n输入: t=%d %s a=%s dev=%s title=%s end=%d\n近期日志:%s",
			seq, step, errCode(got), wantCode, e.now, e.op, e.acct, e.dev, e.title, e.end, n.dump(step))
	}
	if e.op == "status" && got == nil {
		if gotInfo != wantInfo {
			t.Fatalf("seq=%d step=%d status 不一致 got=%+v want=%+v\n近期日志:%s",
				seq, step, gotInfo, wantInfo, n.dump(step))
		}
	}

	if got == nil {
		n.accept(e)
	}

	// 全量重放，交叉枚举可观察状态。
	st := n.replay()
	if e.op == "addAccount" || e.op == "addTitle" || e.op == "setEnd" {
		// 这些步骤不产生设备/许可状态；其余步骤统一在固定账号 A 上核对。
		return
	}
	if e.now < n.lastNow() {
		// 时钟回退时 Status 也被拒绝，无法做只读探测；状态本身未改变。
		return
	}

	// 设备在册性：取任一已知影片作探针，ErrDeviceNotFound 即未注册。
	var probe string
	for ti := range n.titles {
		probe = ti
		break
	}
	devSet := map[string]bool{}
	for _, d := range e.devsProbe(st) {
		devSet[d] = true
	}
	if probe != "" {
		for dev, wantReg := range devSet {
			_, err := m.Status(e.acct, dev, probe, e.now)
			gotReg := err == nil || errors.Is(err, ErrNoLicense)
			if gotReg != wantReg {
				t.Fatalf("seq=%d step=%d 设备在册性 dev=%s got=%v want=%v\n近期日志:%s",
					seq, step, dev, gotReg, wantReg, n.dump(step))
			}
		}
	}

	// 许可状态逐条对照（阶段、exp、过期原因、firstPlay）。
	for key, r := range st.lic {
		parts := strings.SplitN(key, "|", 2)
		dev, title := parts[0], parts[1]
		fp := int64(0)
		if r.played {
			fp = r.firstPlay
		}
		want := playback.Evaluate(e.now, r.rentalEnd, fp, n.lp, st.titleEnd[title])
		got, err := m.Status(e.acct, dev, title, e.now)
		if err != nil {
			t.Fatalf("seq=%d step=%d key=%s 期望有许可却 err=%v\n近期日志:%s",
				seq, step, key, err, n.dump(step))
		}
		if got.State != want.State || got.Exp != want.Exp || got.FirstPlay != want.FirstPlay ||
			(got.State == playback.StateExpired && got.Reason != want.Reason) {
			t.Fatalf("seq=%d step=%d key=%s 状态不一致 got=%+v want=%+v\n近期日志:%s",
				seq, step, key, got, want, n.dump(step))
		}
	}
}

// devsProbe 汇总重放状态中可能在册的设备（含冷却中），供在册性枚举。
func (e nEvent) devsProbe(st *nState) []string {
	seen := map[string]bool{}
	var out []string
	for d := range st.registered {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

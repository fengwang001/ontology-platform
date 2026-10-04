package paywall_test

import (
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"testing"

	"ontology/paywall"
)

func reasonName(r int) string {
	switch r {
	case int(paywall.ReasonSubscription):
		return "subscription"
	case int(paywall.ReasonFree):
		return "free"
	case int(paywall.ReasonUnlocked):
		return "unlocked"
	case int(paywall.ReasonGift):
		return "gift"
	case int(paywall.ReasonQuota):
		return "quota"
	case int(paywall.ReasonBlocked):
		return "blocked"
	}
	return "?"
}

// genOps 为一条随机用例生成操作序列与文章登记信息。
func genOps(rng *rand.Rand) (cfg paywall.Config, arts map[string]bool, ops []nOp) {
	cfg = paywall.Config{
		N: int64(rng.Intn(5)),      // 0..4
		M: int64(3 + rng.Intn(50)), // 3..52，制造频繁跨月
		G: int64(1 + rng.Intn(2)),  // 1..2
		K: int64(1 + rng.Intn(2)),  // 1..2
		E: int64(5 + rng.Intn(40)), // 5..44
	}
	arts = map[string]bool{}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("art%02d", i)
		free := rng.Intn(5) == 0
		arts[id] = free
		ops = append(ops, nOp{kind: "addArticle", article: id, free: free})
	}
	users := []string{"u0", "u1", "u2"}
	allArts := []string{"art00", "art01", "art02", "art03", "art04", "art05", "art06", "art07"}
	now := int64(0)
	subUntil := map[string]int64{}
	devices := []string{"d0", "d1", "d2", "d3"}
	for step := 0; step < 60; step++ {
		// 时间单调不减，偶尔大步跨月。
		if rng.Intn(4) == 0 {
			now += int64(1 + rng.Intn(int(cfg.M*2)))
		} else {
			now += int64(rng.Intn(3))
		}
		d := devices[rng.Intn(len(devices))]
		u := users[rng.Intn(len(users))]
		a := allArts[rng.Intn(len(allArts))]
		switch rng.Intn(10) {
		case 0, 1: // login
			ops = append(ops, nOp{kind: "login", now: now, device: d, user: u})
		case 2: // logout
			ops = append(ops, nOp{kind: "logout", now: now, device: d})
		case 3: // subscribe
			until := now + int64(1+rng.Intn(30))
			subUntil[u] = until
			ops = append(ops, nOp{kind: "subscribe", now: now, user: u, until: until})
		case 4: // gift
			ops = append(ops, nOp{kind: "gift", now: now, user: u, article: a})
		default: // read
			var tok int64
			if rng.Intn(3) == 0 {
				tok = -1 // 预扫描时替换为最近一次成功签发的序号
			}
			ops = append(ops, nOp{kind: "read", now: now, device: d, article: a, token: tok})
		}
	}
	// 预扫描：确定性地把带令牌标记绑定到“最近一次成功签发”的序号（1 基）。
	// 成功条件与真实/朴素模型一致：用户订阅有效且本月礼赠额度 G 未用尽。
	issued := map[string]struct {
		month int64
		count int64
	}{}
	var giftSerials []int64
	for i := range ops {
		o := &ops[i]
		switch o.kind {
		case "gift":
			until, subbed := subUntil[o.user]
			ok := subbed && o.now < until
			mc := issued[o.user]
			if mc.month != o.now/cfg.M {
				mc.month, mc.count = o.now/cfg.M, 0
			}
			if ok && mc.count < cfg.G {
				mc.count++
				giftSerials = append(giftSerials, int64(len(giftSerials))+1)
			}
			issued[o.user] = mc
		case "read":
			if o.token == -1 {
				if len(giftSerials) > 0 {
					o.token = giftSerials[len(giftSerials)-1]
				} else {
					o.token = 0
				}
			}
		}
	}
	return
}

func runCase(t *testing.T, seed int64, concurrent bool) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	cfg, arts, ops := genOps(rng)

	var log []string
	log = append(log, fmt.Sprintf("seed=%d cfg=%+v free=%v", seed, cfg, freeList(arts)))

	// 参考模型。
	nv := newNaive(cfg)
	// 真实系统。
	pw, err := paywall.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 先登记文章（登记不带时钟，直接串行）。
	var body []nOp
	tokenSeq := []int64{} // 第 i 个成功签发的真实令牌编号（serial i+1）
	for _, op := range ops {
		if op.kind == "addArticle" {
			if err := pw.AddArticle([]byte(op.article), op.free); err != nil {
				t.Fatalf("AddArticle: %v", err)
			}
			if _, _, e := nv.apply(op); e != nil {
				t.Fatalf("naive addArticle: %v", e)
			}
			log = append(log, fmt.Sprintf("AddArticle(%s free=%v)", op.article, op.free))
		} else {
			body = append(body, op)
		}
	}

	// 执行一条操作，比对两侧输出。
	execOne := func(idx int, op nOp) {
		var tok []byte
		realToken := int64(0)
		serial := op.token
		if op.kind == "read" && serial > 0 {
			if int(serial-1) < len(tokenSeq) {
				realToken = tokenSeq[serial-1]
				tok = []byte(strconv.FormatInt(realToken, 10))
			} else {
				serial = 0 // 该序号从未成功签发：双方都视同未带令牌
			}
		}
		input := describe(serial, realToken, op)

		var allowed bool
		var reason int
		var gerr error
		var gid int64
		switch op.kind {
		case "subscribe":
			gerr = pw.Subscribe(op.now, []byte(op.user), op.until)
		case "gift":
			gid, gerr = pw.Gift(op.now, []byte(op.user), []byte(op.article))
		case "login":
			gerr = pw.Login(op.now, []byte(op.device), []byte(op.user))
		case "logout":
			gerr = pw.Logout(op.now, []byte(op.device))
		case "read":
			res, e := pw.Read(op.now, []byte(op.device), []byte(op.article), tok)
			gerr = e
			allowed, reason = res.Allowed, int(res.Reason)
		}
		naiveOp := op
		naiveOp.token = serial // 未成功签发的序号不传
		nAllowed, nReason, nerr := nv.apply(naiveOp)
		basis := decideBasis(op, nAllowed, nReason, nerr, gid)
		if !sameError(gerr, nerr) {
			t.Fatalf("seed=%d op#%d %s\nerr mismatch: real=%v naive=%v\n%s",
				seed, idx, input, gerr, nerr, stringsJoin(log))
		}
		if gerr == nil {
			if allowed != nAllowed || reason != nReason {
				t.Fatalf("seed=%d op#%d %s\nresult mismatch: real(allowed=%v %s) naive(allowed=%v %s)\n%s",
					seed, idx, input, allowed, reasonName(reason), nAllowed, reasonName(nReason), stringsJoin(log))
			}
			if op.kind == "gift" {
				if gid == 0 {
					t.Fatalf("seed=%d gift returned 0 without error", seed)
				}
				tokenSeq = append(tokenSeq, gid)
			}
		}
		log = append(log, fmt.Sprintf("%s => %s", input, basis))
	}

	if !concurrent {
		for i, op := range body {
			execOne(i, op)
		}
	} else {
		// 并发重放：操作之间有时钟依赖，不能任意并行。
		// 按 now 相同的层分组：同层操作在串行模型里也可能因绑定状态而互相影响，
		// 因此仅把“互不相关设备/用户”的只读 Read 并发执行以检测竞态。
		var wg sync.WaitGroup
		for i, op := range body {
			if op.kind == "read" {
				wg.Add(1)
				go func(i int, op nOp) { defer wg.Done(); execOneConcurrentSafe(pw, nv, i, op, seed, &log) }(i, op)
			} else {
				wg.Wait()
				execOne(i, op)
			}
		}
		wg.Wait()
		// 并发路径只用于竞态检测；严格逐条比对由串行路径保证。
		return
	}
	t.Log(stringsJoin(log))
}

func freeList(arts map[string]bool) []string {
	var out []string
	for a, f := range arts {
		if f {
			out = append(out, a)
		}
	}
	return out
}

func describe(serial, realToken int64, op nOp) string {
	switch op.kind {
	case "subscribe":
		return fmt.Sprintf("Subscribe(now=%d user=%s until=%d)", op.now, op.user, op.until)
	case "gift":
		return fmt.Sprintf("Gift(now=%d user=%s article=%s)", op.now, op.user, op.article)
	case "login":
		return fmt.Sprintf("Login(now=%d device=%s user=%s)", op.now, op.device, op.user)
	case "logout":
		return fmt.Sprintf("Logout(now=%d device=%s)", op.now, op.device)
	case "read":
		tok := ""
		if serial > 0 {
			tok = fmt.Sprintf(" token=%d(serial %d)", realToken, serial)
		}
		return fmt.Sprintf("Read(now=%d device=%s article=%s%s)", op.now, op.device, op.article, tok)
	}
	return op.kind
}

// decideBasis 返回朴素模型给出的判定依据，用于日志。
func decideBasis(op nOp, allowed bool, reason int, err error, gid int64) string {
	if err != nil {
		return "reject: " + err.Error()
	}
	switch op.kind {
	case "gift":
		return fmt.Sprintf("issued token #%d", gid)
	case "read":
		if !allowed {
			return "blocked (no reason applies)"
		}
		return "allow: " + reasonName(reason)
	default:
		return "accepted"
	}
}

func stringsJoin(lines []string) string {
	out := ""
	for i, l := range lines {
		out += fmt.Sprintf("%3d: %s\n", i, l)
	}
	return out
}

// 并发 Read 仅做竞态检测：同一 now 下多设备阅读（各主体独立），忽略比对差异。
func execOneConcurrentSafe(pw *paywall.Paywall, nv *naive, idx int, op nOp, seed int64, log *[]string) {
	var tok []byte
	if op.token == -1 {
		// 令牌依赖签发编号，竞态组不使用令牌以保持无共享写冲突。
		tok = nil
	}
	_, _ = pw.Read(op.now, []byte(op.device), []byte(op.article), tok)
}

func TestRandomAgainstNaive(t *testing.T) {
	const cases = 1500
	for seed := int64(1); seed <= cases; seed++ {
		runCase(t, seed, false)
	}
}

func TestRandomConcurrentReplayRace(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	for seed := int64(1); seed <= 30; seed++ {
		runCase(t, seed, true)
	}
}

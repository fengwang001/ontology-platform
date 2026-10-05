package ladder

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/reward"
	"ontology/season"
)

// naiveSystem 朴素模拟：每次结算都做全量排序，名次按定义
// （严格更高分人数+1）逐个统计，作为被测系统的对照。
type naiveSystem struct {
	cfg     reward.Config
	players map[string]*reward.Player
	frozen  bool
	settled bool
	ts      int64
	entries map[string]*reward.Entry
	clock   int64
}

const testMaxNow = int64(1_000_000_000_000)

func newNaive(cfg reward.Config) *naiveSystem {
	return &naiveSystem{cfg: cfg, players: map[string]*reward.Player{}}
}

func (n *naiveSystem) player(name string) *reward.Player {
	p, ok := n.players[name]
	if !ok {
		p = &reward.Player{Score: n.cfg.Base}
		n.players[name] = p
	}
	return p
}

func (n *naiveSystem) report(now int64, w, l string) error {
	if now < 0 || now > testMaxNow || w == "" || l == "" || w == l {
		return ErrInvalidParam
	}
	if now < n.clock {
		return ErrClockRollback
	}
	if n.frozen {
		return season.ErrSeasonFrozen
	}
	wp, lp := n.player(w), n.player(l)
	wp.Score += n.cfg.W
	lp.Score -= n.cfg.L
	if lp.Score < 0 {
		lp.Score = 0
	}
	wp.Games++
	lp.Games++
	n.clock = now
	return nil
}

func (n *naiveSystem) settle(now int64) error {
	if now < 0 || now > testMaxNow {
		return ErrInvalidParam
	}
	if now < n.clock {
		return ErrClockRollback
	}
	if n.frozen {
		return season.ErrSeasonFrozen
	}
	type kv struct {
		name  string
		score int64
	}
	var qual []kv
	n.entries = map[string]*reward.Entry{}
	for name, p := range n.players {
		if p.Games >= n.cfg.GMin {
			qual = append(qual, kv{name, p.Score})
		} else {
			n.entries[name] = &reward.Entry{Tier: -1}
		}
	}
	sort.Slice(qual, func(i, j int) bool { return qual[i].score > qual[j].score })
	total := int64(len(qual))
	for _, q := range qual {
		rank := int64(1)
		for _, o := range qual {
			if o.score > q.score {
				rank++
			}
		}
		tier := 0
		if rank != 1 {
			tier = len(n.cfg.Tiers) - 1
			for j, tr := range n.cfg.Tiers {
				if rank*1000 <= total*tr.P {
					tier = j
					break
				}
			}
		}
		amount := n.cfg.Tiers[tier].A
		if rank <= n.cfg.K {
			amount += n.cfg.AK
		}
		n.entries[q.name] = &reward.Entry{Rank: rank, Tier: tier, Amount: amount, Qualified: true}
	}
	n.settled = true
	n.ts = now
	n.frozen = true
	n.clock = now
	return nil
}

func (n *naiveSystem) start(now int64) error {
	if now < 0 || now > testMaxNow {
		return ErrInvalidParam
	}
	if now < n.clock {
		return ErrClockRollback
	}
	if !n.frozen {
		return season.ErrNotFrozen
	}
	for _, p := range n.players {
		d := (p.Score - n.cfg.Base) * n.cfg.Rho
		q := d / 100
		if d%100 != 0 && d < 0 {
			q--
		}
		p.Score = n.cfg.Base + q
		p.Games = 0
	}
	n.frozen = false
	n.clock = now
	return nil
}

func (n *naiveSystem) claim(now int64, player string) (int64, error) {
	if now < 0 || now > testMaxNow || player == "" {
		return 0, ErrInvalidParam
	}
	if now < n.clock {
		return 0, ErrClockRollback
	}
	if !n.settled {
		return 0, ErrNoSettlement
	}
	e, ok := n.entries[player]
	if !ok {
		return 0, reward.ErrNotInSettlement
	}
	if now >= n.ts+n.cfg.Wc {
		return 0, reward.ErrWindowExpired
	}
	if !e.Qualified {
		return 0, reward.ErrNotQualified
	}
	if e.Claimed {
		return 0, reward.ErrAlreadyClaimed
	}
	e.Claimed = true
	n.clock = now
	return e.Amount, nil
}

func (n *naiveSystem) result(player string) (reward.Entry, bool) {
	if !n.settled {
		return reward.Entry{}, false
	}
	e, ok := n.entries[player]
	if !ok {
		return reward.Entry{}, false
	}
	return *e, true
}

type randOp struct {
	kind string
	now  int64
	a, b string
}

func genConfig(rng *rand.Rand) reward.Config {
	nt := 1 + rng.Intn(3)
	tiers := make([]reward.Tier, 0, nt)
	amount := rng.Int63n(2000)
	for i := 0; i < nt; i++ {
		p := int64(i+1) * 1000 / int64(nt)
		tiers = append(tiers, reward.Tier{P: p, A: amount})
		amount = rng.Int63n(amount + 1) // 非增
	}
	return reward.Config{
		Base:  rng.Int63n(1001),
		W:     1 + rng.Int63n(200),
		L:     1 + rng.Int63n(200),
		GMin:  rng.Int63n(4),
		K:     rng.Int63n(6),
		AK:    rng.Int63n(2000),
		Tiers: tiers,
		Rho:   rng.Int63n(101),
		Wc:    1 + rng.Int63n(2000),
	}
}

func genOps(rng *rand.Rand) []randOp {
	pool := []string{"p0", "p1", "p2", "p3", "p4", "p5"}
	pick := func() string { return pool[rng.Intn(len(pool))] }
	ops := make([]randOp, 0, 30)
	lastNow := int64(0)
	for i := 0; i < 30; i++ {
		var now int64
		switch r := rng.Intn(100); {
		case r < 70:
			now = lastNow + rng.Int63n(30)
		case r < 80:
			now = lastNow
		case r < 92:
			now = lastNow - 1 - rng.Int63n(60) // 回退, 可能为负
		default:
			now = testMaxNow + 1 + rng.Int63n(100) // 非法
		}
		if now > lastNow {
			lastNow = now
		}
		switch k := rng.Intn(100); {
		case k < 45:
			a, b := pick(), pick()
			if rng.Intn(100) < 5 {
				b = a
			}
			if rng.Intn(100) < 3 {
				a = ""
			}
			ops = append(ops, randOp{"report", now, a, b})
		case k < 57:
			ops = append(ops, randOp{"settle", now, "", ""})
		case k < 69:
			ops = append(ops, randOp{"start", now, "", ""})
		case k < 94:
			a := pick()
			if rng.Intn(100) < 10 {
				a = "ghost"
			}
			ops = append(ops, randOp{"claim", now, a, ""})
		default:
			ops = append(ops, randOp{"result", now, pick(), ""})
		}
	}
	return ops
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return errors.Is(a, b)
}

func errStr(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

// runSequence 重放一条随机序列, 返回每步结果（用于确定性校验）。
func runSequence(t *testing.T, seq int) []string {
	rng := rand.New(rand.NewSource(int64(seq)))
	cfg := genConfig(rng)
	sys, err := New(cfg.Base, cfg.W, cfg.L, cfg.GMin, cfg.K, cfg.AK, cfg.Tiers, cfg.Rho, cfg.Wc)
	if err != nil {
		t.Fatalf("seq=%d New: %v", seq, err)
	}
	outs := []string{}
	for _, o := range genOps(rng) {
		switch o.kind {
		case "report":
			outs = append(outs, errStr(sys.Report(o.now, o.a, o.b)))
		case "settle":
			outs = append(outs, errStr(sys.Settle(o.now)))
		case "start":
			outs = append(outs, errStr(sys.Start(o.now)))
		case "claim":
			amt, err := sys.Claim(o.now, o.a)
			outs = append(outs, fmt.Sprintf("%d %s", amt, errStr(err)))
		case "result":
			e, ok := sys.Result(o.a)
			outs = append(outs, fmt.Sprintf("%+v %v", e, ok))
		}
	}
	return outs
}

func TestRandomVsNaive(t *testing.T) {
	for seq := 0; seq < 1500; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		cfg := genConfig(rng)
		sys, err := New(cfg.Base, cfg.W, cfg.L, cfg.GMin, cfg.K, cfg.AK, cfg.Tiers, cfg.Rho, cfg.Wc)
		if err != nil {
			t.Fatalf("seq=%d New: %v", seq, err)
		}
		nv := newNaive(cfg)
		for i, o := range genOps(rng) {
			in := fmt.Sprintf("%s(now=%d,a=%q,b=%q)", o.kind, o.now, o.a, o.b)
			switch o.kind {
			case "report":
				ge, ne := sys.Report(o.now, o.a, o.b), nv.report(o.now, o.a, o.b)
				t.Logf("seq=%d op=%02d in=%s out=%s why=%s", seq, i, in, errStr(ge), errStr(ne))
				if !sameErr(ge, ne) {
					t.Fatalf("seq=%d op=%02d %s: 系统=%v 朴素=%v", seq, i, in, ge, ne)
				}
			case "settle":
				ge, ne := sys.Settle(o.now), nv.settle(o.now)
				t.Logf("seq=%d op=%02d in=%s out=%s why=%s", seq, i, in, errStr(ge), errStr(ne))
				if !sameErr(ge, ne) {
					t.Fatalf("seq=%d op=%02d %s: 系统=%v 朴素=%v", seq, i, in, ge, ne)
				}
				if ge == nil {
					checkSettlementInvariants(t, seq, sys)
				}
			case "start":
				ge, ne := sys.Start(o.now), nv.start(o.now)
				t.Logf("seq=%d op=%02d in=%s out=%s why=%s", seq, i, in, errStr(ge), errStr(ne))
				if !sameErr(ge, ne) {
					t.Fatalf("seq=%d op=%02d %s: 系统=%v 朴素=%v", seq, i, in, ge, ne)
				}
			case "claim":
				gAmt, ge := sys.Claim(o.now, o.a)
				nAmt, ne := nv.claim(o.now, o.a)
				t.Logf("seq=%d op=%02d in=%s out=(%d,%s) why=%s", seq, i, in, gAmt, errStr(ge), errStr(ne))
				if !sameErr(ge, ne) || gAmt != nAmt {
					t.Fatalf("seq=%d op=%02d %s: 系统=(%d,%v) 朴素=(%d,%v)", seq, i, in, gAmt, ge, nAmt, ne)
				}
			case "result":
				ge, gok := sys.Result(o.a)
				ne, nok := nv.result(o.a)
				t.Logf("seq=%d op=%02d in=%s out=(%+v,%v) why=对照朴素结果", seq, i, in, ge, gok)
				if gok != nok || ge != ne {
					t.Fatalf("seq=%d op=%02d %s: 系统=(%+v,%v) 朴素=(%+v,%v)", seq, i, in, ge, gok, ne, nok)
				}
			}
		}
	}
}

// checkSettlementInvariants 不变量: 同分同档同酬, 分高者奖励不低于分低者。
func checkSettlementInvariants(t *testing.T, seq int, sys *System) {
	type qa struct {
		score int64
		entry reward.Entry
	}
	var qs []qa
	for name, p := range sys.players {
		e, ok := sys.Result(name)
		if !ok || !e.Qualified {
			continue
		}
		qs = append(qs, qa{p.Score, e})
	}
	for i := 0; i < len(qs); i++ {
		for j := i + 1; j < len(qs); j++ {
			a, b := qs[i], qs[j]
			if a.score == b.score && (a.entry.Tier != b.entry.Tier || a.entry.Amount != b.entry.Amount) {
				t.Fatalf("seq=%d 同分不同酬: %d分 %+v vs %+v", seq, a.score, a.entry, b.entry)
			}
			if a.score > b.score && a.entry.Amount < b.entry.Amount {
				t.Fatalf("seq=%d 分高者奖励更低: %d分得%d < %d分得%d",
					seq, a.score, a.entry.Amount, b.score, b.entry.Amount)
			}
		}
	}
}

func TestReplayDeterministic(t *testing.T) {
	for seq := 0; seq < 50; seq++ {
		first := runSequence(t, seq)
		second := runSequence(t, seq)
		if len(first) != len(second) {
			t.Fatalf("seq=%d 重放长度不一致", seq)
		}
		for i := range first {
			if first[i] != second[i] {
				t.Fatalf("seq=%d op=%d 重放不一致: %q vs %q", seq, i, first[i], second[i])
			}
		}
	}
}

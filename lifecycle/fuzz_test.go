package lifecycle

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
)

// naive 是完全按题目规则逐步手写的独立参考模型（不复用生产代码的计算路径）。
type naive struct {
	cfg    Config
	maxNow int64
	period int64
	used   int64
	objs   map[string][2]int64 // key -> {size, la}
}

func newNaive(c Config) *naive {
	return &naive{cfg: c, objs: map[string][2]int64{}}
}

func nlayer(la, now, A, B int64) int {
	d := now - la
	if d < A {
		return LayerHot
	}
	if d < B {
		return LayerCool
	}
	return LayerCold
}

func nsettle(c Config, size, la, now int64) Fees {
	d := now - la
	hot := d
	if hot > c.A {
		hot = c.A
	}
	cool := int64(0)
	if d >= c.A {
		cool = d - c.A
		if cool > c.B-c.A {
			cool = c.B - c.A
		}
	}
	cold := int64(0)
	if d > c.B {
		cold = d - c.B
	}
	storage := big.NewInt(size)
	storage.Mul(storage, big.NewInt(c.P0*hot+c.P1*cool+c.P2*cold))

	exit := big.NewInt(0)
	switch nlayer(la, now, c.A, c.B) {
	case LayerCool:
		need := c.M1 - (d - c.A)
		if need > 0 {
			exit.SetInt64(need * c.P1 * size)
		}
	case LayerCold:
		need := c.M2 - (d - c.B)
		if need > 0 {
			exit.SetInt64(need * c.P2 * size)
		}
	}
	return Fees{Storage: storage, Exit: exit, Retriev: big.NewInt(0)}
}

func (n *naive) get(key string, now int64) (Fees, error) {
	obj, ok := n.objs[key]
	if !ok {
		return zeroFees(), ErrNotFound
	}
	size, la := obj[0], obj[1]
	f := nsettle(n.cfg, size, la, now)
	if l := nlayer(la, now, n.cfg.A, n.cfg.B); l != LayerHot {
		p := now / 30
		if p != n.period {
			n.period = p
			n.used = 0
		}
		rate := n.cfg.R1
		if l == LayerCold {
			rate = n.cfg.R2
		}
		fg := size
		if rem := n.cfg.Q - n.used; fg > rem {
			fg = rem
		}
		n.used += fg
		f.Retriev = big.NewInt((size - fg) * rate)
	}
	n.objs[key] = [2]int64{size, now}
	n.maxNow = now
	return f, nil
}

func (n *naive) put(key string, size, now int64) Fees {
	f := zeroFees()
	if old, ok := n.objs[key]; ok {
		f = nsettle(n.cfg, old[0], old[1], now)
	}
	n.objs[key] = [2]int64{size, now}
	n.maxNow = now
	return f
}

func (n *naive) del(key string, now int64) (Fees, error) {
	obj, ok := n.objs[key]
	if !ok {
		return zeroFees(), ErrNotFound
	}
	f := nsettle(n.cfg, obj[0], obj[1], now)
	delete(n.objs, key)
	n.maxNow = now
	return f, nil
}

type op struct {
	kind int // 0 put, 1 delete, 2 get, 3 getmany
	key  string
	size int64
	now  int64
	keys []string
}

func (n *naive) validKey(k string) bool { return k != "" && len(k) <= 64 }

// runNaive 按与生产实现相同的拒绝顺序执行参考模型。
func runNaive(n *naive, o op) ([]Fees, error) {
	switch o.kind {
	case 3:
		for _, k := range o.keys {
			if !n.validKey(k) {
				return nil, ErrInvalidArgs
			}
		}
		if o.now < 0 || o.now > 1e9 {
			return nil, ErrInvalidArgs
		}
		if o.now < n.maxNow {
			return nil, ErrClockRollback
		}
		for _, k := range o.keys {
			if _, ok := n.objs[k]; !ok {
				return nil, ErrNotFound
			}
		}
		out := make([]Fees, len(o.keys))
		for i, k := range o.keys {
			out[i], _ = n.get(k, o.now)
		}
		return out, nil
	case 0:
		if !n.validKey(o.key) || o.size < 1 || o.size > 1e6 || o.now < 0 || o.now > 1e9 {
			return nil, ErrInvalidArgs
		}
		if o.now < n.maxNow {
			return nil, ErrClockRollback
		}
		return []Fees{n.put(o.key, o.size, o.now)}, nil
	default:
		if !n.validKey(o.key) || o.now < 0 || o.now > 1e9 {
			return nil, ErrInvalidArgs
		}
		if o.now < n.maxNow {
			return nil, ErrClockRollback
		}
		var (
			f   Fees
			err error
		)
		if o.kind == 1 {
			f, err = n.del(o.key, o.now)
		} else {
			f, err = n.get(o.key, o.now)
		}
		return []Fees{f}, err
	}
}

func validRandomArgs(o op) bool {
	if o.key == "" || len(o.key) > 64 || o.size < 1 || o.size > 1e6 ||
		o.now < 0 || o.now > 1e9 {
		return false
	}
	for _, k := range o.keys {
		if k == "" || len(k) > 64 {
			return false
		}
	}
	return true
}

func randomConfig(r *rand.Rand) Config {
	A := int64(1 + r.Intn(60))
	B := A + int64(1+r.Intn(120))
	return Config{
		P0: int64(r.Intn(20)), P1: int64(r.Intn(20)), P2: int64(r.Intn(20)),
		A: A, B: B,
		M1: int64(r.Intn(120)), M2: int64(r.Intn(160)),
		R1: int64(r.Intn(10)), R2: int64(r.Intn(10)),
		Q: int64(r.Intn(30)),
	}
}

func repeatStr(s string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(s)
	}
	return b.String()
}

func randomOps(r *rand.Rand, n int) []op {
	keys := []string{"a", "b", "c", "d", "k", "x1"}
	badKeys := []string{"", "长键-" + repeatStr("z", 70)}
	ops := make([]op, 0, n)
	now := int64(0)
	for i := 0; i < n; i++ {
		kind := r.Intn(4)
		switch r.Intn(10) {
		case 0:
			now -= int64(1 + r.Intn(5))
			if now < 0 {
				now = 0
			}
		case 9:
			now += int64(1 + r.Intn(120))
		default:
			now += int64(r.Intn(40))
		}
		o := op{kind: kind, now: now, size: int64(1 + r.Intn(20))}
		if r.Intn(15) == 0 {
			o.key = badKeys[r.Intn(len(badKeys))]
		} else {
			o.key = keys[r.Intn(len(keys))]
		}
		if kind == 3 {
			cnt := 1 + r.Intn(3)
			for j := 0; j < cnt; j++ {
				pick := keys[r.Intn(len(keys))]
				if r.Intn(8) == 0 {
					pick = "missing"
				}
				o.keys = append(o.keys, pick)
			}
		}
		ops = append(ops, o)
	}
	return ops
}

func feeListEqual(a, c []Fees) bool {
	if len(a) != len(c) {
		// 被拒绝的操作两侧费用都应视为“无费用”（可能为 nil 或零值包装）。
		return len(a) == 0 || len(c) == 0
	}
	for i := range a {
		if !feesEqual(a[i], c[i]) {
			return false
		}
	}
	return true
}

func sameErr(a, b error) bool {
	return errors.Is(a, ErrInvalidArgs) == errors.Is(b, ErrInvalidArgs) &&
		errors.Is(a, ErrClockRollback) == errors.Is(b, ErrClockRollback) &&
		errors.Is(a, ErrNotFound) == errors.Is(b, ErrNotFound) &&
		(a != nil) == (b != nil)
}

func errName(e error) string {
	switch {
	case errors.Is(e, ErrInvalidArgs):
		return "InvalidArgs"
	case errors.Is(e, ErrClockRollback):
		return "ClockRollback"
	case errors.Is(e, ErrNotFound):
		return "NotFound"
	default:
		return "nil"
	}
}

func kindName(k int) string {
	return [...]string{"Put", "Delete", "Get", "GetMany"}[k]
}

func fmtFees(fs []Fees) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = fmt.Sprintf("{s=%s,e=%s,r=%s}", f.Storage, f.Exit, f.Retriev)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

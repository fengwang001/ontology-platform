package ontology

import (
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// -logdiff 打开后打印每次操作的输入、输出与逐诊断判定依据。
var logDiff = flag.Bool("logdiff", false, "打印随机对照的输入、输出与判定依据")

func timeSeed() int64 { return time.Now().UnixNano() }

func diffLogger(t *testing.T) func(string, ...any) {
	t.Helper()
	if !*logDiff {
		return func(string, ...any) {}
	}
	return func(format string, args ...any) { t.Logf(format, args...) }
}

// TestRandomDifferential 用随机目录变更、保单登记与理赔序列，
// 逐操作比对引擎与独立朴素模型的错误、赔付、逐诊断判定及档案。
func TestRandomDifferential(t *testing.T) {
	seed := timeSeed()
	rng := rand.New(rand.NewSource(seed))
	logf := diffLogger(t)
	logf("=== differential seed=%d ===", seed)

	for iter := 0; iter < 400; iter++ {
		e := NewEngine()
		m := newNaive()

		// 先造一片随机目录：编号 c0..cN，多数带父（父编号更小，保证森林）。
		n := 2 + rng.Intn(18)
		type codeInfo struct {
			parent     string
			accidental bool
		}
		infos := make([]codeInfo, n)
		accRoots := map[int]bool{}
		for i := 0; i < n; i++ {
			var parent string
			acc := rng.Intn(5) == 0
			if i > 0 && rng.Intn(3) != 0 {
				p := rng.Intn(i)
				parent = fmt.Sprintf("c%d", p)
				if accRoots[p] {
					acc = false // 父已是意外根子树，自身标记与否结果相同
				}
			}
			if acc {
				accRoots[i] = true
			}
			infos[i] = codeInfo{parent, acc}
		}
		// 父编号始终小于子编号，按编号顺序登记即可保证父先于子。
		for i := 0; i < n; i++ {
			code := fmt.Sprintf("c%d", i)
			ee := e.AddCode(code, infos[i].parent, infos[i].accidental)
			me := m.addCode(code, infos[i].parent, infos[i].accidental)
			if errKey(ee) != errKey(me) {
				t.Fatalf("seed=%d addCode(%s,%s,%v) engine=%v naive=%v",
					seed, code, infos[i].parent, infos[i].accidental, ee, me)
			}
		}

		// 若干被保人，登记保单链与理赔。
		users := []string{"u1", "u2", "u3"}
		// 记录每名被保人保单末尾，用于生成衔接续保。
		lastReg := map[string]int{}
		lastExp := map[string]int{}
		hasPol := map[string]bool{}
		claimSeq := 0

		ops := 60
		for op := 0; op < ops; op++ {
			u := users[rng.Intn(len(users))]
			switch rng.Intn(3) {
			case 0, 1:
				// 登记保单：首单任意；之后以一定概率生成衔接续保，否则错开区间。
				in := RegisterPolicyInput{Insured: u, Amount: 100 + rng.Intn(3000)}
				if hasPol[u] && rng.Intn(2) == 0 {
					in.Effective = lastExp[u]
					if rng.Intn(5) == 0 {
						in.RegisterAt = lastExp[u] + 1 // 晚一天登记 -> 新投保
					} else {
						in.RegisterAt = lastExp[u] - rng.Intn(3) // 到期日之前（含当天）
					}
					in.Amount = 100 + rng.Intn(4000)
				} else {
					in.Effective = rng.Intn(200)
					in.RegisterAt = rng.Intn(200)
				}
				in.Expiry = in.Effective + 1 + rng.Intn(30)
				in.WaitDays = rng.Intn(8)
				// 告知：随机抽若干已存在编码，偶发塞入不存在编码以触发拒绝。
				k := rng.Intn(3)
				for j := 0; j < k; j++ {
					if rng.Intn(8) == 0 {
						in.Disclosure = append(in.Disclosure, "ghost")
					} else {
						in.Disclosure = append(in.Disclosure, fmt.Sprintf("c%d", rng.Intn(n)))
					}
				}
				ee := e.RegisterPolicy(in)
				me := m.register(in)
				logf("op=%d register %+v => engine=%v naive=%v", op, in, ee, me)
				if errKey(ee) != errKey(me) {
					t.Fatalf("seed=%d register 不一致: in=%+v engine=%v naive=%v", seed, in, ee, me)
				}
				if ee == nil {
					hasPol[u] = true
					lastReg[u] = in.RegisterAt
					lastExp[u] = in.Expiry
				}
			default:
				// 理赔：可能针对无保单用户、不存在编码、重复号、未承保日。
				claimSeq++
				id := fmt.Sprintf("cl%d", claimSeq)
				if rng.Intn(6) == 0 {
					id = "dup" // 复用号触发重复（第二次起）
				}
				day := rng.Intn(260)
				dn := 1 + rng.Intn(3)
				var ds []Diagnosis
				for j := 0; j < dn; j++ {
					code := fmt.Sprintf("c%d", rng.Intn(n))
					if rng.Intn(10) == 0 {
						code = "no-such-code"
					}
					charge := rng.Intn(600) - 10 // 偶发非正
					if charge <= 0 {
						charge = 1 + rng.Intn(500)
					}
					if rng.Intn(15) == 0 {
						charge = 0
					}
					ds = append(ds, diag(code, charge))
				}
				er, ee := e.SubmitClaim(id, u, day, ds)
				mr, me := m.claim(id, u, day, ds)
				logf("op=%d claim id=%s u=%s day=%d ds=%v => engine=(%v,%+v) naive=(%v,%+v)",
					op, id, u, day, ds, ee, er, me, mr)
				if errKey(ee) != errKey(me) {
					t.Fatalf("seed=%d claim 错误不一致: id=%s u=%s day=%d ds=%v engine=%v naive=%v",
						seed, id, u, day, ds, ee, me)
				}
				if ee == nil {
					if er.Paid != mr.paid || len(er.Diagnoses) != len(mr.diags) {
						t.Fatalf("seed=%d 赔付/诊断数不一致: engine=%+v naive=%+v", seed, er, mr)
					}
					for i := range mr.diags {
						if er.Diagnoses[i].Code != mr.diags[i].code ||
							er.Diagnoses[i].Verdict != mr.diags[i].verdict {
							t.Fatalf("seed=%d 诊断判定不一致: engine=%+v naive=%+v",
								seed, er.Diagnoses, mr.diags)
						}
					}
				}
			}

			// 每轮后比对全部被保人档案。
			for _, u := range users {
				es := e.ArchiveSnapshot(u)
				mset := m.archive[u]
				if len(es) != len(mset) {
					t.Fatalf("seed=%d op=%d 档案不一致 u=%s engine=%v naive=%v",
						seed, op, u, es, mset)
				}
				for _, c := range es {
					if _, ok := mset[c]; !ok {
						t.Fatalf("seed=%d op=%d 档案成员不一致 u=%s code=%s", seed, op, u, c)
					}
				}
			}
		}
	}
}

func errKey(err error) string {
	if err == nil {
		return ""
	}
	for _, s := range []*CodeError{
		ErrInvalidParam, ErrInsuredNotFound, ErrCodeNotFound, ErrCodeDuplicate,
		ErrParentNotFound, ErrOverlap, ErrClaimExists, ErrNotInsured,
	} {
		if errors.Is(err, s) {
			return s.Error()
		}
	}
	return err.Error()
}

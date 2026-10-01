package swiss

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

type badReport struct {
	a, b, r int
}

// randomBadReport 构造一条应被拒绝的登记：非法 r、未知种子、
// a==b、轮空者或非本轮对阵；在 r 合法的前提下返回具体情形。
func randomBadReport(rng *rand.Rand, pairs []Pair, bye, n int) badReport {
	for {
		switch rng.Intn(4) {
		case 0:
			return badReport{1, 2, 3 + rng.Intn(5)} // r 非法（种子是否有效不影响判定顺序）
		case 1:
			return badReport{1, n + 1 + rng.Intn(3), 2} // 未知种子
		case 2:
			return badReport{1, 1, 2} // a==b
		default:
			if bye != 0 {
				p := pairs[0]
				other := p.X
				if other == bye {
					other = p.Y
				}
				return badReport{bye, other, 2} // 涉及轮空者
			}
			if len(pairs) >= 2 {
				return badReport{pairs[0].X, pairs[1].Y, 2} // 都不是轮空者但不构成对阵
			}
		}
	}
}

// TestRandomDifferential 用 2000 组随机比赛过程对照真实实现与朴素实现。
// 每组过程包含合法与非法操作；逐步比对错误类别、对阵、轮空与榜单，
// 日志记录每条输入、双方输出与判定依据；出现分歧时打印整组日志。
func TestRandomDifferential(t *testing.T) {
	const trials = 2000
	rng := rand.New(rand.NewSource(20261001))

	for trial := 0; trial < trials; trial++ {
		maxR := 1 + rng.Intn(6)
		nPlayers := 1 + rng.Intn(8)

		real, err := New(maxR)
		if err != nil {
			t.Fatalf("trial %d: New: %v", trial, err)
		}
		ref := newRefModel(maxR)

		var log []string
		log = append(log, fmt.Sprintf("trial=%d R=%d declaredN=%d", trial, maxR, nPlayers))

		// 登记阶段：多数唯一，少量空名/重名（按拒绝顺序只报第一个）。
		var accepted []string
		for i := 0; i < nPlayers; i++ {
			var name string
			switch rng.Intn(10) {
			case 0:
				name = ""
			case 1:
				if len(accepted) > 0 {
					name = accepted[rng.Intn(len(accepted))]
				} else {
					name = fmt.Sprintf("P%d-%d", trial, i)
				}
			default:
				name = fmt.Sprintf("P%d-%d", trial, i)
			}
			_, e1 := real.Register(name)
			_, e2 := ref.reg(name)
			log = append(log, fmt.Sprintf("Register(%q) -> real=%v naive=%v", name, e1, e2))
			if !sameErr(e1, e2) {
				t.Fatalf("trial %d: Register mismatch\n%s", trial, strings.Join(log, "\n"))
			}
			if e1 == nil {
				accepted = append(accepted, name)
			}
		}

		// 逐轮推进；多调用一次 Pair 以覆盖达到 R 轮后的拒绝。
		for round := 0; round <= maxR+1; round++ {
			rp, rb, re1 := real.Pair()
			fp, fb, re2 := ref.pair()
			log = append(log, fmt.Sprintf("Pair() -> real{pairs=%v bye=%d err=%v} naive{pairs=%v bye=%d err=%v}",
				rp, rb, re1, fp, fb, re2))
			if !sameErr(re1, re2) || rb != fb {
				t.Fatalf("trial %d round %d: Pair mismatch\n%s", trial, round, strings.Join(log, "\n"))
			}
			if re1 != nil {
				// 失败不得产生副作用。
				if !reflect.DeepEqual(real.Standings(), ref.standings()) {
					t.Fatalf("trial %d: standings after failed Pair differ\n%s", trial, strings.Join(log, "\n"))
				}
				if round <= maxR {
					break // ErrNoPairing：过程终止
				}
				continue // ErrMaxRounds 后再确认榜单即可
			}
			var wantPairs []Pair
			for _, p := range fp {
				wantPairs = append(wantPairs, Pair{X: p[0], Y: p[1]})
			}
			if !reflect.DeepEqual(rp, wantPairs) {
				t.Fatalf("trial %d round %d: pairs %v vs %v\n%s", trial, round, rp, wantPairs, strings.Join(log, "\n"))
			}

			order := rng.Perm(len(wantPairs))
			for k, idx := range order {
				p := wantPairs[idx]
				// 夹杂一条非法登记，验证错误分类与无副作用。
				if rng.Intn(3) == 0 {
					bad := randomBadReport(rng, wantPairs, fb, len(ref.names))
					ge := real.Report(bad.a, bad.b, bad.r)
					fe := ref.report(bad.a, bad.b, bad.r)
					log = append(log, fmt.Sprintf("Report(%d,%d,%d)[bad] -> real=%v naive=%v（判定：应按规则顺序拒绝且不改状态）",
						bad.a, bad.b, bad.r, ge, fe))
					if !sameErr(ge, fe) {
						t.Fatalf("trial %d: bad Report mismatch\n%s", trial, strings.Join(log, "\n"))
					}
					if !reflect.DeepEqual(real.Standings(), ref.standings()) {
						t.Fatalf("trial %d: rejected report changed standings\n%s", trial, strings.Join(log, "\n"))
					}
				}

				res := rng.Intn(3)
				a, b := p.X, p.Y
				if rng.Intn(2) == 1 {
					a, b = p.Y, p.X
				}
				ge := real.Report(a, b, res)
				fe := ref.report(a, b, res)
				log = append(log, fmt.Sprintf("Report(%d,%d,%d) -> real=%v naive=%v（判定：%d 得 %d、%d 得 %d）",
					a, b, res, ge, fe, a, res, b, 2-res))
				if ge != nil || fe != nil {
					t.Fatalf("trial %d: valid Report rejected real=%v naive=%v\n%s", trial, ge, fe, strings.Join(log, "\n"))
				}

				// 已登记盘再报：尚未到本轮最后一盘时应报 ErrAlreadyScore。
				if k == 0 && len(wantPairs) > 1 {
					ge := real.Report(p.X, p.Y, res)
					fe := ref.report(p.X, p.Y, res)
					log = append(log, fmt.Sprintf("Report(%d,%d,%d)[dup] -> real=%v naive=%v", p.X, p.Y, res, ge, fe))
					if !sameErr(ge, fe) || !errors.Is(ge, ErrAlreadyScore) {
						t.Fatalf("trial %d: dup Report mismatch real=%v naive=%v\n%s", trial, ge, fe, strings.Join(log, "\n"))
					}
				}

				// 同一轮内登记顺序不影响（结束时的）榜单。
				if rs2, fs2 := real.Standings(), ref.standings(); !reflect.DeepEqual(rs2, fs2) {
					t.Fatalf("trial %d mid-round standings differ: real=%v naive=%v\n%s", trial, rs2, fs2, strings.Join(log, "\n"))
				}
			}

			rs := real.Standings()
			fs := ref.standings()
			log = append(log, fmt.Sprintf("Standings() -> real=%v naive=%v", rs, fs))
			if !reflect.DeepEqual(rs, fs) {
				t.Fatalf("trial %d round %d: standings mismatch\n%s", trial, round, strings.Join(log, "\n"))
			}
			games, byes := countGamesByes(ref)
			total := 0
			for _, x := range fs {
				total += x.Score
			}
			if want := 2*games + 2*byes; total != want {
				t.Fatalf("trial %d: total score %d != 2*%d+2*%d=%d\n%s",
					trial, total, games, byes, want, strings.Join(log, "\n"))
			}
		}

		if err := real.Report(1, 2, 2); !errors.Is(err, ErrNoRound) {
			t.Fatalf("trial %d: report after completion: %v\n%s", trial, err, strings.Join(log, "\n"))
		}
	}
	t.Logf("differential complete: %d trials（每组日志含输入、输出与判定依据；失败时整组打印）", trials)
}

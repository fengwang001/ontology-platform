package session_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/caps"
	"ontology/session"
)

type naiveResult struct {
	reason  caps.Reason
	feat    int
	ver     uint64
	enabled uint32
}

// naivePick 按题目规则逐版本枚举：先 NoVersion，再 Missing，再 Denied，
// 再从低到高找同时满足全部必需特性的版本，取最高；E 为该版本可用全部特性。
func naivePick(feats []caps.Feature, ch caps.Hello, cr uint8, sh caps.Hello, extra uint32) naiveResult {
	L := maxU(ch.Lo, sh.Lo)
	H := minU(ch.Hi, sh.Hi)
	if L > H {
		return naiveResult{reason: caps.ReasonNoVersion, feat: -1}
	}
	common := ch.Sup & sh.Sup
	q := ch.Req | sh.Req | extra
	for f := 0; f < caps.FeatureCount; f++ {
		if q&(1<<uint(f)) != 0 && common&(1<<uint(f)) == 0 {
			return naiveResult{reason: caps.ReasonMissing, feat: f}
		}
	}
	for f := 0; f < caps.FeatureCount; f++ {
		if q&(1<<uint(f)) != 0 && feats[f].Role > cr {
			return naiveResult{reason: caps.ReasonDenied, feat: f}
		}
	}
	ok := func(v uint64) bool {
		for f := 0; f < caps.FeatureCount; f++ {
			if q&(1<<uint(f)) != 0 {
				x := feats[f]
				if v < x.MinV || v >= x.MaxV {
					return false
				}
			}
		}
		return true
	}
	var highest uint64
	found := false
	consider := func(v uint64) {
		if v >= L && v <= H && ok(v) && (!found || v > highest) {
			highest, found = v, true
		}
	}
	if H-L+1 <= 64 {
		for v := L; v <= H; v++ {
			consider(v)
		}
	} else {
		consider(L)
		consider(H)
		for _, x := range feats {
			consider(x.MinV)
			consider(x.MaxV - 1)
		}
	}
	if !found {
		return naiveResult{reason: caps.ReasonWindow, feat: -1}
	}
	var e uint32
	for f := 0; f < caps.FeatureCount; f++ {
		b := uint32(1 << uint(f))
		x := feats[f]
		if common&b != 0 && x.Role <= cr && highest >= x.MinV && highest < x.MaxV {
			e |= b
		}
	}
	return naiveResult{reason: caps.ReasonOK, feat: -1, ver: highest, enabled: e}
}

func maxU(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func minU(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

type modelSess struct {
	ver     uint64
	enabled uint32
	using   uint32
	ch      caps.Hello
	cr      uint8
}

func randFeats(rng *rand.Rand, span uint64) []caps.Feature {
	feats := make([]caps.Feature, caps.FeatureCount)
	for i := range feats {
		if span <= 64 {
			lo := uint64(rng.Intn(int(span))) + 1
			hi := lo + 1 + uint64(rng.Intn(int(span)+2))
			if hi > span+1 {
				hi = span + 1
			}
			feats[i] = caps.Feature{MinV: lo, MaxV: hi, Role: uint8(rng.Intn(3))}
		} else {
			points := []uint64{1, 2, 5, 10, 500_000_000, 999_999_999, 1_000_000_000}
			loIdx := rng.Intn(len(points) - 1)
			lo := points[loIdx]
			hi := points[loIdx+1+rng.Intn(len(points)-1-loIdx)]
			if rng.Intn(3) == 0 || hi == 1_000_000_000 {
				hi = 1_000_000_001
			}
			feats[i] = caps.Feature{MinV: lo, MaxV: hi, Role: uint8(rng.Intn(3))}
		}
	}
	return feats
}

func randHello(rng *rand.Rand, span uint64) caps.Hello {
	lo := uint64(rng.Intn(int(span))) + 1
	hi := lo + uint64(rng.Intn(int(span-lo)+1))
	sup := rng.Uint32()
	req := sup & rng.Uint32()
	return caps.Hello{Lo: lo, Hi: hi, Sup: sup, Req: req}
}

func pickSID(rng *rand.Rand, m map[uint64]*modelSess) uint64 {
	keys := make([]uint64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys[rng.Intn(len(keys))]
}

// TestNaive 对拍 2000 组随机声明与操作序列，跨度 10 与 10^9 各半。
// 失败时打印该组输入、操作日志与判定依据（管理器日志）。
func TestNaive(t *testing.T) {
	const groups = 2000
	for g := 0; g < groups; g++ {
		span := uint64(10)
		mode := "span10"
		if g%2 == 1 {
			span, mode = 1_000_000_000, "span1e9"
		}
		rng := rand.New(rand.NewSource(int64(g + 1)))
		feats := randFeats(rng, span)
		tab, err := caps.NewTable(feats)
		if err != nil {
			t.Fatalf("group %d: table: %v", g, err)
		}

		log := &strings.Builder{}
		fmt.Fprintf(log, "group %d %s\n", g, mode)
		for i, f := range feats {
			fmt.Fprintf(log, "  f%d={min:%d max:%d role:%d}\n", i, f.MinV, f.MaxV, f.Role)
		}

		m := session.NewManager(tab)
		buf := &strings.Builder{}
		session.SetLogOutput(buf)

		server := caps.EmptyHello()
		sessions := map[uint64]*modelSess{}
		var nextSID uint64 = 1

		failf := func(format string, args ...any) {
			t.Helper()
			t.Fatalf(format+"\n---- scenario ----\n%s---- engine log ----\n%s",
				append(args, log.String(), buf.String())...)
		}
		checkErr := func(step string, got error, want caps.Reason, feat int) {
			t.Helper()
			ge := caps.AsError(got)
			if want == caps.ReasonOK {
				if got != nil {
					failf("group %d %s: unexpected error %v", g, step, got)
				}
				return
			}
			if ge == nil || ge.Reason != want || ge.Feature != feat {
				gf, gr := -1, caps.ReasonOK
				if ge != nil {
					gf, gr = ge.Feature, ge.Reason
				}
				failf("group %d %s: want reason=%d feat=%d got reason=%d feat=%d (%v)",
					g, step, want, feat, gr, gf, got)
			}
		}

		const ops = 24
		for op := 0; op < ops; op++ {
			switch rng.Intn(7) {
			case 0:
				role := []uint8{0, 1, 2, 2, 2}[rng.Intn(5)]
				h := randHello(rng, span)
				want := caps.ReasonOK
				if role != 2 {
					want = caps.ReasonForbidden
				} else if e := caps.ValidateHello(h); e != nil {
					want = caps.ReasonInvalid
				}
				fmt.Fprintf(log, "op%d SetServer role=%d h=%+v want=%d\n", op, role, h, want)
				checkErr("SetServer", m.SetServer(role, h), want, -1)
				if want == caps.ReasonOK {
					server = h
				}
			case 1, 2:
				h := randHello(rng, span)
				cr := uint8(rng.Intn(4))
				wantReason, wantFeat := caps.ReasonOK, -1
				if e := caps.ValidateHello(h); e != nil {
					wantReason = caps.ReasonInvalid
				} else if cr > 2 {
					wantReason = caps.ReasonInvalid
				} else {
					nr := naivePick(feats, h, cr, server, 0)
					wantReason, wantFeat = nr.reason, nr.feat
				}
				fmt.Fprintf(log, "op%d Negotiate h=%+v cr=%d server=%+v want=%d/%d\n",
					op, h, cr, server, wantReason, wantFeat)
				sid, info, err := m.Negotiate(h, cr)
				checkErr("Negotiate", err, wantReason, wantFeat)
				if wantReason == caps.ReasonOK {
					nr := naivePick(feats, h, cr, server, 0)
					if sid != nextSID || info.Ver != nr.ver || info.Enabled != nr.enabled || info.Using != 0 {
						failf("group %d Negotiate mismatch sid=%d %+v want sid=%d v=%d E=%08x",
							g, sid, info, nextSID, nr.ver, nr.enabled)
					}
					sessions[sid] = &modelSess{ver: nr.ver, enabled: nr.enabled, ch: h, cr: cr}
					nextSID++
				}
			case 3:
				if len(sessions) == 0 {
					break
				}
				sid := pickSID(rng, sessions)
				f := uint8(rng.Intn(caps.FeatureCount + 1))
				s := sessions[sid]
				want, feat := caps.ReasonOK, -1
				if int(f) >= caps.FeatureCount {
					want, feat = caps.ReasonInvalid, int(f)
				} else if s.enabled&(1<<f) == 0 {
					want, feat = caps.ReasonNotEnabled, int(f)
				}
				fmt.Fprintf(log, "op%d Use sid=%d f=%d want=%d\n", op, sid, f, want)
				checkErr("Use", m.Use(sid, f), want, feat)
				if want == caps.ReasonOK {
					s.using |= 1 << f
				}
			case 4:
				if len(sessions) == 0 {
					break
				}
				sid := pickSID(rng, sessions)
				s := sessions[sid]
				nr := naivePick(feats, s.ch, s.cr, server, s.using)
				fmt.Fprintf(log, "op%d Renegotiate sid=%d U=%08x server=%+v want=%d/%d v=%d E=%08x\n",
					op, sid, s.using, server, nr.reason, nr.feat, nr.ver, nr.enabled)
				info, err := m.Renegotiate(sid)
				checkErr("Renegotiate", err, nr.reason, nr.feat)
				if nr.reason == caps.ReasonOK {
					if info.Ver != nr.ver || info.Enabled != nr.enabled || info.Using != s.using {
						failf("group %d Renegotiate mismatch %+v want v=%d E=%08x",
							g, info, nr.ver, nr.enabled)
					}
					if s.using&^nr.enabled != 0 {
						failf("group %d: U=%08x not subset of new E=%08x", g, s.using, nr.enabled)
					}
					s.ver, s.enabled = nr.ver, nr.enabled
				} else {
					got, e := m.Info(sid)
					if e != nil || got.Ver != s.ver || got.Enabled != s.enabled || got.Using != s.using {
						failf("group %d: failed renegotiate mutated session: %+v (%v)", g, got, e)
					}
				}
			case 5:
				if len(sessions) == 0 {
					break
				}
				sid := pickSID(rng, sessions)
				fmt.Fprintf(log, "op%d Close sid=%d\n", op, sid)
				checkErr("Close", m.Close(sid), caps.ReasonOK, -1)
				delete(sessions, sid)
				err := m.Use(sid, 0)
				checkErr("Use-after-Close", err, caps.ReasonNoSession, -1)
			case 6:
				if len(sessions) == 0 {
					break
				}
				sid := pickSID(rng, sessions)
				s := sessions[sid]
				info, err := m.Info(sid)
				checkErr("Info", err, caps.ReasonOK, -1)
				if info.Ver != s.ver || info.Enabled != s.enabled || info.Using != s.using {
					failf("group %d Info mismatch %+v want v=%d E=%08x U=%08x",
						g, info, s.ver, s.enabled, s.using)
				}
				if info.Using&^info.Enabled != 0 {
					failf("group %d invariant U subset of E broken", g)
				}
			}
		}
		if g%500 == 0 {
			t.Logf("group %d (%s) ok", g, mode)
		}
	}
}

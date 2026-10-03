package session_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/caps"
	"ontology/session"
)

// modelSess 是朴素模型中保存的会话。
type modelSess struct {
	ch      caps.Hello
	cr      int
	version int
	enabled uint32
	using   uint32
}

type modelState struct {
	tbl    [caps.NumFeatures]caps.Spec
	server caps.Hello
	next   uint64
	live   map[uint64]*modelSess
}

// naiveNegotiate 逐特性按规则重算，判定次序与题面一致；
// E 的计算直接按定义逐特性判定，不依赖实现中的任何代码。
func naiveNegotiate(tbl [caps.NumFeatures]caps.Spec, cl caps.Hello, cr int, srv caps.Hello, extra uint32) (int, uint32, *caps.Error) {
	lo := cl.Lo
	if srv.Lo > lo {
		lo = srv.Lo
	}
	hi := cl.Hi
	if srv.Hi < hi {
		hi = srv.Hi
	}
	if lo > hi {
		return 0, 0, caps.ErrNoVersion
	}
	q := cl.Req | srv.Req | extra
	common := cl.Sup & srv.Sup
	for f := 0; f < caps.NumFeatures; f++ {
		if q&(1<<uint(f)) != 0 && common&(1<<uint(f)) == 0 {
			return 0, 0, caps.ErrFeature(caps.CodeMissing, f)
		}
	}
	for f := 0; f < caps.NumFeatures; f++ {
		if q&(1<<uint(f)) != 0 && tbl[f].Role > cr {
			return 0, 0, caps.ErrFeature(caps.CodeDenied, f)
		}
	}
	lower, upper := lo, hi
	for f := 0; f < caps.NumFeatures; f++ {
		if q&(1<<uint(f)) == 0 {
			continue
		}
		if tbl[f].MinV > lower {
			lower = tbl[f].MinV
		}
		if tbl[f].MaxV-1 < upper {
			upper = tbl[f].MaxV - 1
		}
	}
	if lower > upper {
		return 0, 0, caps.ErrWindow
	}
	v := upper
	var en uint32
	// 逐版本/逐特性的朴素判定：f 可用 iff minV <= v < maxV。
	for f := 0; f < caps.NumFeatures; f++ {
		b := uint32(1) << uint(f)
		if common&b != 0 && tbl[f].Role <= cr && tbl[f].MinV <= v && v < tbl[f].MaxV {
			en |= b
		}
	}
	return v, en, nil
}

func randHello(r *rand.Rand, maxV int, all uint32) caps.Hello {
	lo := r.Intn(maxV) + 1
	hi := lo + r.Intn(maxV-lo+1)
	sup := all & r.Uint32()
	req := sup & r.Uint32()
	return caps.Hello{Lo: lo, Hi: hi, Sup: sup, Req: req}
}

func TestDifferential_2000RandomSequences(t *testing.T) {
	const N = 2000
	rng := rand.New(rand.NewSource(20261003))

	for iter := 0; iter < N; iter++ {
		maxV := 12 // 小规模窗口，保证朴素规则可直接枚举
		var tbl [caps.NumFeatures]caps.Spec
		for f := range tbl {
			minV := rng.Intn(maxV) + 1
			maxVf := minV + 1 + rng.Intn(maxV)
			if rng.Intn(4) == 0 {
				maxVf = caps.Unbounded
			}
			tbl[f] = caps.Spec{MinV: minV, MaxV: maxVf, Role: rng.Intn(3)}
		}
		table, e := caps.NewTable(tbl)
		if e != nil {
			t.Fatalf("iter %d NewTable: %v", iter, e)
		}
		mgr, _ := session.NewManager(table)
		md := &modelState{
			tbl:    tbl,
			server: caps.Hello{Lo: 1, Hi: 1},
			next:   1,
			live:   map[uint64]*modelSess{},
		}

		logf := func(format string, args ...any) {
			if iter < 5 || t.Failed() {
				t.Logf("[iter %d] %s", iter, fmt.Sprintf(format, args...))
			}
		}

		all := uint32(0xFFFFFFFF)
		for op := 0; op < 40; op++ {
			switch rng.Intn(6) {
			case 0: // SetServer，随机角色以覆盖权限拒绝
				role := []int{0, 1, 2, 2, 2}[rng.Intn(5)]
				h := randHello(rng, maxV, all)
				before := md.server
				got := mgr.SetServer(role, h)
				wantOK := role == 2
				if wantOK != (got == nil) {
					t.Fatalf("iter %d SetServer role=%d: %v", iter, role, got)
				}
				if wantOK {
					md.server = h
				}
				logf("SetServer(role=%d, %+v) -> %v; applied=%v (server kept %+v otherwise)", role, h, got, wantOK, before)
			case 1: // Negotiate
				h := randHello(rng, maxV, all)
				cr := rng.Intn(3)
				sid, res, got := mgr.Negotiate(h, cr)
				v, en, want := naiveNegotiate(md.tbl, h, cr, md.server, 0)
				if (got == nil) != (want == nil) || got != nil && (got.Code != want.Code || got.Feature != want.Feature) {
					t.Fatalf("iter %d Negotiate mismatch: got=%v want=%v", iter, got, want)
				}
				if got == nil {
					if sid != md.next || res.Version != v || res.Enabled != en {
						t.Fatalf("iter %d result mismatch sid=%d/%d v=%d/%d E=%b/%b", iter, sid, md.next, res.Version, v, res.Enabled, en)
					}
					md.live[sid] = &modelSess{ch: h, cr: cr, version: v, enabled: en}
					md.next++
				}
				logf("Negotiate(%+v, cr=%d) -> sid=%d v=%d E=%08x err=%v; reason L=%d H=%d Q=%08x",
					h, cr, sid, res.Version, res.Enabled, got,
					maxI(h.Lo, md.server.Lo), minI(h.Hi, md.server.Hi), h.Req|md.server.Req)
			case 2: // Use
				if len(md.live) == 0 {
					break
				}
				sid, ms := pickLive(rng, md)
				f := rng.Intn(caps.NumFeatures + 2) // 偶发越界
				got := mgr.Use(sid, f)
				var want *caps.Error
				switch {
				case f < 0 || f >= caps.NumFeatures:
					want = caps.ErrFeature(caps.CodeInvalid, f)
				case ms.enabled&(1<<uint(f)) == 0:
					want = caps.ErrFeature(caps.CodeNotEnabled, f)
				}
				assertErr(t, iter, "Use", got, want)
				if want == nil {
					ms.using |= 1 << uint(f)
				}
				logf("Use(sid=%d, f=%d) -> %v (E=%08x U=%08x)", sid, f, got, ms.enabled, ms.using)
			case 3: // Renegotiate
				sid, ms, present := anyLive(rng, md)
				res, got := mgr.Renegotiate(sid)
				if !present {
					assertErr(t, iter, "Renegotiate", got, caps.ErrNoSession)
					logf("Renegotiate(sid=%d) -> %v (dead)", sid, got)
					break
				}
				v, en, want := naiveNegotiate(md.tbl, ms.ch, ms.cr, md.server, ms.using)
				if (got == nil) != (want == nil) || got != nil && (got.Code != want.Code || got.Feature != want.Feature) {
					t.Fatalf("iter %d Reneg mismatch: got=%v want=%v", iter, got, want)
				}
				if got == nil {
					if res.Version != v || res.Enabled != en {
						t.Fatalf("iter %d Reneg result: v=%d/%d E=%b/%b", iter, res.Version, v, res.Enabled, en)
					}
					if ms.using&^en != 0 {
						t.Fatalf("iter %d model invariant broken U=%b E=%b", iter, ms.using, en)
					}
					ms.version, ms.enabled = v, en
				}
				logf("Renegotiate(sid=%d, U=%08x) -> v=%d E=%08x err=%v", sid, ms.using, res.Version, res.Enabled, got)
			case 4: // Info
				sid, ms, present := anyLive(rng, md)
				v, en, u, got := mgr.Info(sid)
				if !present {
					assertErr(t, iter, "Info", got, caps.ErrNoSession)
					break
				}
				if got != nil || v != ms.version || en != ms.enabled || u != ms.using {
					t.Fatalf("iter %d Info mismatch: (%d,%b,%b,%v) model (%d,%b,%b)",
						iter, v, en, u, got, ms.version, ms.enabled, ms.using)
				}
			case 5: // Close
				sid, _, present := anyLive(rng, md)
				got := mgr.Close(sid)
				if present {
					if got != nil {
						t.Fatalf("iter %d Close: %v", iter, got)
					}
					delete(md.live, sid)
				} else {
					assertErr(t, iter, "Close", got, caps.ErrNoSession)
				}
				logf("Close(sid=%d) -> %v", sid, got)
			}
		}

		// 终态：所有存活会话的 Info 必须与模型一致。
		for sid, ms := range md.live {
			v, en, u, got := mgr.Info(sid)
			if got != nil || v != ms.version || en != ms.enabled || u != ms.using || u&^en != 0 {
				t.Fatalf("iter %d final state sid=%d: (%d,%b,%b) model (%d,%b,%b)",
					iter, sid, v, en, u, ms.version, ms.enabled, ms.using)
			}
		}
	}
}

func assertErr(t *testing.T, iter int, op string, got, want *caps.Error) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("iter %d %s: got=%v want=%v", iter, op, got, want)
	}
	if got != nil && (got.Code != want.Code || got.Feature != want.Feature) {
		t.Fatalf("iter %d %s: got=%v(%d) want=%v(%d)", iter, op, got, got.Feature, want, want.Feature)
	}
}

func pickLive(r *rand.Rand, md *modelState) (uint64, *modelSess) {
	ids := make([]uint64, 0, len(md.live))
	for id := range md.live {
		ids = append(ids, id)
	}
	id := ids[r.Intn(len(ids))]
	return id, md.live[id]
}

// anyLive 以约 1/4 概率选不存在/已关闭的 sid，覆盖 ErrNoSession。
func anyLive(r *rand.Rand, md *modelState) (uint64, *modelSess, bool) {
	if len(md.live) > 0 && r.Intn(4) != 0 {
		id, ms := pickLive(r, md)
		return id, ms, true
	}
	return md.next + uint64(r.Intn(3)), nil, false
}

func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}

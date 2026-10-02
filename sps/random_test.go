package sps

import (
	"fmt"
	"math/rand"
	"testing"
)

// verifyAgainst 用朴素模型全量核对当前距离与父边，并返回不一致描述。
func verifyAgainst(t *testing.T, svc *Service, nm *naiveModel, seq, step int, op string) {
	t.Helper()
	for v := 0; v < svc.n; v++ {
		d, ok, err := svc.Dist(v)
		if err != nil {
			t.Fatalf("seq%d step%d %s Dist err: %v", seq, step, op, err)
		}
		nok := nm.dist[v] != inf
		if ok != nok || (ok && d != nm.dist[v]) {
			t.Fatalf("seq%d step%d %s dist(%d): inc=(%d,%v) naive=(%d,%v)",
				seq, step, op, v, d, ok, nm.dist[v], nok)
		}
		p, pok, err := svc.Parent(v)
		if err != nil {
			t.Fatal(err)
		}
		if p != nm.par[v] || pok != (nm.par[v] != 0) {
			t.Fatalf("seq%d step%d %s parent(%d): inc=(%d,%v) naive=%d",
				seq, step, op, v, p, pok, nm.par[v])
		}
		// 父边不变量：可达非源点父边必为活边且紧。
		if ok && v != svc.source {
			if p == 0 {
				t.Fatalf("seq%d step%d reachable %d has no parent", seq, step, v)
			}
			pe := svc.edges[p]
			if pe == nil || !pe.alive {
				t.Fatalf("seq%d step%d parent edge %d not alive", seq, step, p)
			}
			if d != svc.dist[pe.u]+pe.w {
				t.Fatalf("seq%d step%d parent edge %d not tight", seq, step, p)
			}
		}
	}
	// Path 不变量：沿父边到源，权重和等于距离。
	if t.Failed() {
		return
	}
	for v := 0; v < svc.n; v++ {
		if nm.dist[v] == inf {
			if _, err := svc.Path(v); err != ErrUnreachable {
				t.Fatalf("seq%d Path(%d) err=%v, want ErrUnreachable", seq, v, err)
			}
			continue
		}
		path, err := svc.Path(v)
		if err != nil {
			t.Fatalf("seq%d Path(%d): %v", seq, v, err)
		}
		var sum int64
		cur := svc.source
		for _, pid := range path {
			pe := svc.edges[pid]
			if pe.u != cur {
				t.Fatalf("seq%d Path(%d) discontinuous at edge %d", seq, v, pid)
			}
			cur = pe.v
			sum += pe.w
		}
		if cur != v || sum != nm.dist[v] {
			t.Fatalf("seq%d Path(%d)=%v ends %d sum %d, dist %d", seq, v, path, cur, sum, nm.dist[v])
		}
	}
}

func diffLists(a, b []int) bool { return !eqInts(a, b) }

// expectedReports 按题目口径由朴素前后状态计算 DChanged/PChanged。
func expectedReports(prevDist []int64, prevPar []int, nm *naiveModel) (dc, pc []int) {
	for v := 0; v < nm.n; v++ {
		pd, nd := prevDist[v], nm.dist[v]
		prevOK, nowOK := pd != inf, nd != inf
		if prevOK != nowOK || (prevOK && pd != nd) {
			dc = append(dc, v)
			continue
		}
		if v != nm.s && nowOK && prevPar[v] != nm.par[v] {
			pc = append(pc, v)
		}
	}
	return dc, pc
}

// TestRandomAgainstNaive 2000 组随机更新序列，每组与朴素实现逐步对照。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(0x5151*64 + seq)))
		n := 2 + rng.Intn(10)
		emax := 1 + rng.Intn(14)
		k := 1 + rng.Intn(6)
		s := rng.Intn(n)
		svc, err := New(n, s, emax, k)
		if err != nil {
			t.Fatal(err)
		}
		nm := newNaive(n, s)
		steps := 1 + rng.Intn(120)
		nextNaiveID := 1

		t.Logf("seq%d INPUT: New(N=%d,s=%d,Emax=%d,K=%d), steps=%d", seq, n, s, emax, k, steps)

		for step := 0; step < steps; step++ {
			choice := rng.Intn(10)
			var op string
			var res *UpdateResult
			var ierr error
			var prevDist []int64
			var prevPar []int
			prevVer := nm.version

			switch {
			case choice < 5: // AddEdge
				u := rng.Intn(n)
				v := rng.Intn(n)
				w := int64(1 + rng.Intn(40))
				op = fmt.Sprintf("AddEdge(u=%d,v=%d,w=%d)", u, v, w)
				prevDist = nm.snapshotDist()
				prevPar = nm.snapshotPar()
				res, ierr = svc.AddEdge(u, v, w)
				switch {
				case ierr == ErrInvalidArgument:
					t.Logf("seq%d step%d OUTPUT %s -> reject InvalidArgument (naive: rejected)", seq, step, op)
					if u == v || u >= n || v >= n {
						continue
					}
					t.Fatalf("seq%d unexpected invalid add", seq)
				case ierr == ErrCapacityFull:
					t.Logf("seq%d step%d OUTPUT %s -> reject CapacityFull (naive: capacity=%d)", seq, step, op, emax)
					continue
				case ierr != nil:
					t.Fatalf("seq%d add err: %v", seq, ierr)
				default:
					nm.add(res.EdgeID, u, v, w)
					if res.EdgeID != nextNaiveID {
						t.Fatalf("seq%d allocated id %d, want %d", seq, res.EdgeID, nextNaiveID)
					}
					nextNaiveID++
				}
			case choice < 9: // SetWeight
				id := 1 + rng.Intn(nextNaiveID+2) // 含未分配/已删除
				w := int64(1 + rng.Intn(40))
				op = fmt.Sprintf("SetWeight(id=%d,w=%d)", id, w)
				prevDist = nm.snapshotDist()
				prevPar = nm.snapshotPar()
				res, ierr = svc.SetWeight(id, w)
				if ierr == ErrEdgeNotFound {
					t.Logf("seq%d step%d OUTPUT %s -> reject EdgeNotFound (naive: absent)", seq, step, op)
					if _, ok := nm.edges[id]; ok {
						t.Fatalf("seq%d edge %d exists in naive but rejected", seq, id)
					}
					continue
				}
				if ierr != nil {
					t.Fatalf("seq%d setweight: %v", seq, ierr)
				}
				if !nm.setWeight(id, w) {
					t.Fatalf("seq%d naive missing edge %d", seq, id)
				}
			default: // RemoveEdge
				id := 1 + rng.Intn(nextNaiveID+2)
				op = fmt.Sprintf("RemoveEdge(id=%d)", id)
				prevDist = nm.snapshotDist()
				prevPar = nm.snapshotPar()
				res, ierr = svc.RemoveEdge(id)
				if ierr == ErrEdgeNotFound {
					t.Logf("seq%d step%d OUTPUT %s -> reject EdgeNotFound (naive: absent)", seq, step, op)
					if _, ok := nm.edges[id]; ok {
						t.Fatalf("seq%d edge %d exists in naive but rejected", seq, id)
					}
					continue
				}
				if ierr != nil {
					t.Fatalf("seq%d remove: %v", seq, ierr)
				}
				if !nm.remove(id) {
					t.Fatalf("seq%d naive missing edge %d", seq, id)
				}
			}

			// 由朴素前后状态计算期望报告，验证口径一致。
			_ = diffLists
			t.Logf("seq%d step%d OUTPUT %s -> ver=%d id=%d D=%v P=%d examined=%d | JUDGE: compare full dist/parent/path vs naive",
				seq, step, op, res.Version, res.EdgeID, res.DChanged, res.PChanged, res.Examined)

			// 报告口径：必须与朴素前后状态严格一致。
			wantD, wantP := expectedReports(prevDist, prevPar, nm)
			if diffLists(res.DChanged, wantD) {
				t.Fatalf("seq%d step%d %s DChanged=%v want=%v", seq, step, op, res.DChanged, wantD)
			}
			if diffLists(res.PChanged, wantP) {
				t.Fatalf("seq%d step%d %s PChanged=%v want=%v", seq, step, op, res.PChanged, wantP)
			}
			if res.Version != nm.version || res.Version != prevVer+1 {
				t.Fatalf("seq%d step%d version mismatch: %d vs naive %d", seq, step, res.Version, nm.version)
			}

			verifyAgainst(t, svc, nm, seq, step, op)

			// DistAt：未来版本报错；窗口内与朴素历史一致；窗口外报过期。
			if _, _, err := svc.DistAt(0, svc.version+1); err != ErrVersionFuture {
				t.Fatalf("seq%d future version err=%v", seq, err)
			}
			oldest := svc.oldestVersion()
			for ver := 1; ver <= svc.version; ver++ {
				for v := 0; v < n; v++ {
					d, ok, err := svc.DistAt(v, ver)
					if ver < oldest {
						if err != ErrVersionStale {
							t.Fatalf("seq%d DistAt(%d,%d) stale window: err=%v", seq, v, ver, err)
						}
						continue
					}
					if err != nil {
						t.Fatalf("seq%d DistAt(%d,%d): %v", seq, v, ver, err)
					}
					nd := nm.hist[ver][v]
					nok := nd != inf
					if ok != nok || (ok && d != nd) {
						t.Fatalf("seq%d DistAt(%d,%d)=(%d,%v), naive=(%d,%v)", seq, v, ver, d, ok, nd, nok)
					}
				}
			}
			// 版本0：在窗口内时与朴素 v0 一致，否则过期。
			if oldest <= 0 {
				for v := 0; v < n; v++ {
					d, ok, err := svc.DistAt(v, 0)
					if err != nil {
						t.Fatalf("seq%d DistAt(%d,0): %v", seq, v, err)
					}
					nd := nm.hist[0][v]
					if ok != (nd != inf) || (ok && d != nd) {
						t.Fatalf("seq%d DistAt(%d,0) mismatch", seq, v)
					}
				}
			} else if _, _, err := svc.DistAt(0, 0); err != ErrVersionStale {
				t.Fatalf("seq%d v0 should be stale: %v", seq, err)
			}
			if t.Failed() {
				t.Fatalf("seq%d failed at step %d", seq, step)
			}
		}
	}
}

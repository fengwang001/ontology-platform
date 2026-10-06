package reflow

import (
	"math/rand"
	"testing"
)

// fullRecompute 是测试侧独立实现的全量重算：不依赖内核脏标记，
// 按当前属性与子节点自底向上重新计算全部节点（含摘下子树）。
func fullRecompute(t *testing.T, k *Kernel) map[NodeID]Size {
	t.Helper()
	out := make(map[NodeID]Size, len(k.nodes))
	var walk func(n *node)
	walk = func(n *node) {
		for _, c := range n.children {
			walk(c)
		}
		var w, h int64
		if n.width.Kind == ModeFixed {
			w = n.width.Value
		} else {
			for _, c := range n.children {
				w += out[c.id].W
			}
			w += 2 * n.padX
		}
		if n.height.Kind == ModeFixed {
			h = n.height.Value
		} else {
			for _, c := range n.children {
				h += out[c.id].H
			}
			h += 2 * n.padY
		}
		out[n.id] = Size{W: w, H: h}
	}
	walk(k.root)
	return out
}

// attachedToRoot 判断节点当前是否挂在主树上；摘下森林的尺寸按语义冻结，
// 不参与提交后对照。
func attachedToRoot(n *node, root *node) bool {
	for p := n; p != nil; p = p.parent {
		if p == root {
			return true
		}
	}
	return false
}

// op 表示随机测试中的一步操作，内核与朴素模型执行同一 op，
// 成功/失败与错误类别都必须一致。
type op struct {
	kind string
	a, b NodeID
	idx  int
	wm   Mode
	hm   Mode
	px   int64
	py   int64
	iso  bool
}

// TestRandomAgainstNaive 随机操作序列对照：结构、错误类别与提交后尺寸
// 必须与独立编写的朴素模型逐节点一致。
func TestRandomAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		t.Run("seed", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			k := newTestKernel(t)
			m, err := NewNaive(Fixed(100), Fixed(100), 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			kIDs := []NodeID{0}
			mIDs := []NodeID{0}

			mkMode := func() Mode {
				if rng.Intn(2) == 0 {
					return Auto()
				}
				return Fixed(int64(rng.Intn(12)))
			}

			for step := 0; step < 220; step++ {
				var o op
				switch rng.Intn(9) {
				case 0, 1:
					o.kind = "new"
					o.wm, o.hm = mkMode(), mkMode()
					o.px, o.py = int64(rng.Intn(4)), int64(rng.Intn(4))
					o.iso = rng.Intn(4) == 0
					id, ke := k.NewNode(o.wm, o.hm, o.px, o.py, o.iso)
					mid, me := m.NewNode(o.wm, o.hm, o.px, o.py, o.iso)
					if KindOf(ke) != KindOf(me) {
						t.Fatalf("seed=%d step=%d NewNode 错误类别不一致 k=%v m=%v", seed, step, ke, me)
					}
					if ke == nil {
						if id != mid {
							t.Fatalf("id 分配不一致 k=%d m=%d", id, mid)
						}
						kIDs = append(kIDs, id)
						mIDs = append(mIDs, mid)
					}
					continue
				case 2:
					o.kind = "setw"
				case 3:
					o.kind = "seth"
				case 4:
					o.kind = "setp"
				case 5:
					o.kind = "seti"
				case 6:
					o.kind = "insert"
				case 7:
					o.kind = "remove"
				default:
					o.kind = "move"
				}

				pick := func() NodeID {
					return kIDs[rng.Intn(len(kIDs))]
				}
				var ke, me error
				switch o.kind {
				case "setw":
					o.a = pick()
					o.wm = mkMode()
					ke = k.SetWidth(o.a, o.wm)
					me = m.SetWidth(o.a, o.wm)
				case "seth":
					o.a = pick()
					o.hm = mkMode()
					ke = k.SetHeight(o.a, o.hm)
					me = m.SetHeight(o.a, o.hm)
				case "setp":
					o.a = pick()
					o.px, o.py = int64(rng.Intn(5)), int64(rng.Intn(5))
					ke = k.SetPadding(o.a, o.px, o.py)
					me = m.SetPadding(o.a, o.px, o.py)
				case "seti":
					o.a = pick()
					o.iso = rng.Intn(2) == 0
					ke = k.SetIsolated(o.a, o.iso)
					me = m.SetIsolated(o.a, o.iso)
				case "insert":
					// 偶发注入非法参数，验证拒绝次序不破坏状态。
					o.a, o.b = pick(), pick()
					o.idx = rng.Intn(6) - 1
					ke = k.Insert(o.a, o.b, o.idx)
					me = m.Insert(o.a, o.b, o.idx)
				case "remove":
					o.a = pick()
					ke = k.Remove(o.a)
					me = m.Remove(o.a)
				case "move":
					o.a, o.b = pick(), pick()
					o.idx = rng.Intn(6) - 1
					ke = k.Move(o.a, o.b, o.idx)
					me = m.Move(o.a, o.b, o.idx)
				}
				if KindOf(ke) != KindOf(me) {
					t.Fatalf("seed=%d step=%d op=%s 错误类别不一致 k=%v(%v) m=%v(%v)",
						seed, step, o.kind, ke, KindOf(ke), me, KindOf(me))
				}

				if step%7 == 6 {
					if rng.Intn(2) == 0 {
						if _, ke = k.Commit(); ke != nil {
							t.Fatalf("seed=%d commit: %v", seed, ke)
						}
					}
					m.Commit()
					for _, id := range kIDs {
						kn := k.nodes[id]
						if !attachedToRoot(kn, k.root) {
							continue
						}
						ks, _ := k.Size(id)
						ms, _ := m.Size(id)
						if ks != ms {
							t.Fatalf("seed=%d step=%d 节点 %d 尺寸不一致 k=%v m=%v",
								seed, step, id, ks, ms)
						}
					}
				}
			}
			if _, err := k.Commit(); err != nil {
				t.Fatal(err)
			}
			m.Commit()
			got := fullRecompute(t, k)
			for _, id := range kIDs {
				if !attachedToRoot(k.nodes[id], k.root) {
					continue
				}
				ks, _ := k.Size(id)
				ms, _ := m.Size(id)
				if ks != ms || ks != got[id] {
					t.Fatalf("seed=%d 最终节点 %d：内核=%v 朴素=%v 全量=%v",
						seed, id, ks, ms, got[id])
				}
			}
		})
	}
}

// TestLoggingShowsRationale 日志须包含输入、输出与边界/传播判定依据。
func TestLoggingShowsRationale(t *testing.T) {
	k := newTestKernel(t)
	log := &memLogger{}
	k.Log = log
	a := addNode(t, k, Auto(), Auto(), 0, 0, false)
	mustOK(t, k.Insert(0, a, 0))
	_, _ = k.Commit()
	mustOK(t, k.SetPadding(a, 3, 3))
	_, _ = k.Commit()
	text := log.sb.String()
	for _, want := range []string{"SetPadding", "边界=true", "子树含脏", "重算节点", "重排结束"} {
		if !contains(text, want) {
			t.Fatalf("日志缺少判定依据 %q，实际：\n%s", want, text)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestPerformanceProof 可验证地证明两条复杂度性质：
//  1. 一次提交重算节点数不随干净节点总数增长；
//  2. 单次属性修改的标记传播步数只与到最近边界的深度有关。
func TestPerformanceProof(t *testing.T) {
	// 性质 1：内部边界下改一个叶子，新增的大量干净兄弟子树不被重算。
	k := newTestKernel(t)
	b := addNode(t, k, Fixed(80), Fixed(80), 0, 0, true)
	mustOK(t, k.Insert(0, b, 0))
	target := addNode(t, k, Fixed(5), Fixed(5), 0, 0, false)
	mustOK(t, k.Insert(b, target, 0))
	const cleanTrees = 2000
	for i := 0; i < cleanTrees; i++ {
		c := addNode(t, k, Fixed(1), Fixed(1), 0, 0, false)
		leaf := addNode(t, k, Fixed(1), Fixed(1), 0, 0, false)
		mustOK(t, k.Insert(b, c, i+1))
		mustOK(t, k.Insert(c, leaf, 0))
	}
	_, _ = k.Commit()
	k.stats.RecomputeVisits = 0
	mustOK(t, k.SetWidth(target, Fixed(6)))
	_, _ = k.Commit()
	// 仅 target 与边界 b（size 固定但被含脏覆盖）受影响；2000 棵干净子树零参与。
	if visited := k.stats.RecomputeVisits; visited > 2 {
		t.Fatalf("重算节点数应与干净节点数无关，期望<=2，实际 %d", visited)
	}

	// 性质 2：两次传播深度不同（到最近边界），步数差应恰好等于深度差，
	// 与总节点数无关。
	measure := func(depth int) int64 {
		kk := newTestKernel(t)
		var chain []NodeID
		// 每条链都包在同一个内部边界内，最近边界即该边界。
		bb := addNode(t, kk, Fixed(50), Fixed(50), 0, 0, true)
		mustOK(t, kk.Insert(0, bb, 0))
		parent := bb
		for i := 0; i < depth; i++ {
			n := addNode(t, kk, Auto(), Auto(), 0, 0, false)
			mustOK(t, kk.Insert(parent, n, 0))
			chain = append(chain, n)
			parent = n
		}
		_, _ = kk.Commit()
		before := kk.stats.MarkPropSteps
		mustOK(t, kk.SetPadding(chain[len(chain)-1], 1, 1))
		return kk.stats.MarkPropSteps - before
	}
	d5, d15 := measure(5), measure(15)
	if d15-d5 != 10 {
		t.Fatalf("传播步数应只随到边界深度线性增长：depth5=%d depth15=%d，差=%d", d5, d15, d15-d5)
	}
}

package pvbinding

import (
	"math/rand"
	"testing"
)

// TestRandomDifferential 在大量随机卷集与随机操作序列上逐步对照主实现与独立朴素模型：
// 每一步都比较 (成功/失败、错误类别、全量状态指纹)，并在任何时候交叉自检。
func TestRandomDifferential(t *testing.T) {
	const runs, steps = 40, 220
	for seed := int64(1); seed <= runs; seed++ {
		t.Run("seed="+itoa(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			c := New()
			m := newNaive()
			log := newLog(t)

			scs := []string{"gold", "silver"}
			modesPool := []AccessMode{ReadWriteOnce, ReadOnlyMany, ReadWriteMany}
			nodes := []string{"n1", "n2", "n3"}
			modeSet := func() map[AccessMode]bool {
				k := 1 + rng.Intn(3)
				set := map[AccessMode]bool{}
				for len(set) < k {
					set[modesPool[rng.Intn(len(modesPool))]] = true
				}
				return set
			}
			rndName := func(prefix string, n int) string {
				return prefix + itoa(int64(rng.Intn(n)))
			}
			requireSame := func(step int, op string, ctrlOK bool, ctrlErr error, naiveOK bool) {
				t.Helper()
				if ctrlOK != naiveOK {
					t.Fatalf("seed=%d step=%d %s divergence: ctrlOK=%v (%v) naiveOK=%v\nCONTROLLER:\n%s",
						seed, step, op, ctrlOK, ctrlErr, naiveOK,
						controllerFingerprint(c.Snapshot())+"\nNAIVE:\n"+m.fingerprint())
				}
				if cf, nf := controllerFingerprint(c.Snapshot()), m.fingerprint(); cf != nf {
					t.Fatalf("seed=%d step=%d state divergence after %s\nCONTROLLER:\n%s\nNAIVE:\n%s",
						seed, step, op, cf, nf)
				}
				if err := c.CheckInvariants(); err != nil {
					t.Fatalf("seed=%d step=%d invariants: %v", seed, step, err)
				}
			}

			for step := 0; step < steps; step++ {
				op := rng.Intn(9)
				switch op {
				case 0:
					name := rndName("v", 14)
					s := VolumeSpec{
						Capacity:     int64(1 + rng.Intn(20)),
						StorageClass: scs[rng.Intn(2)],
						AccessModes:  modeSet(),
						Reclaim:      []ReclaimPolicy{ReclaimRetain, ReclaimDelete}[rng.Intn(2)],
					}
					if rng.Intn(3) == 0 {
						s.Labels = labels("z", []string{"a", "b"}[rng.Intn(2)])
					}
					if rng.Intn(4) == 0 {
						s.NodeNames = ss(nodes[rng.Intn(len(nodes))])
					}
					if rng.Intn(6) == 0 {
						s.ReservedClaim = rndName("c", 10)
					}
					err := c.AddVolume(name, s)
					ok := m.addVolume(name, s)
					log.step(itoa(int64(step))+" AddVolume "+name, err,
						"对照朴素 addOK="+boolStr(ok))
					requireSame(step, "AddVolume", err == nil, err, ok)
				case 1:
					name := rndName("c", 14)
					s := ClaimSpec{
						RequestCapacity: int64(1 + rng.Intn(20)),
						StorageClass:    scs[rng.Intn(2)],
						AccessModes:     modeSet(),
						BindMode:        []BindMode{BindImmediate, BindWaitForConsumer}[rng.Intn(2)],
					}
					if rng.Intn(4) == 0 {
						s.Selector = labels("z", []string{"a", "b"}[rng.Intn(2)])
					}
					if rng.Intn(8) == 0 {
						s.VolumeName = rndName("v", 14)
					}
					err := c.AddClaim(name, s)
					ok := m.addClaim(name, s)
					log.step(itoa(int64(step))+" AddClaim "+name+" mode="+string(s.BindMode),
						err, "对照朴素 addOK="+boolStr(ok))
					requireSame(step, "AddClaim", err == nil, err, ok)
				case 2:
					name := rndName("c", 14)
					err := c.DeleteClaim(name)
					ok := m.deleteClaim(name)
					log.step(itoa(int64(step))+" DeleteClaim "+name, err,
						"对照朴素 delOK="+boolStr(ok))
					requireSame(step, "DeleteClaim", err == nil, err, ok)
				case 3:
					name := rndName("v", 14)
					s := VolumeSpec{
						Capacity:     int64(1 + rng.Intn(20)),
						StorageClass: scs[rng.Intn(2)],
						AccessModes:  modeSet(),
						Reclaim:      []ReclaimPolicy{ReclaimRetain, ReclaimDelete}[rng.Intn(2)],
					}
					if rng.Intn(4) == 0 {
						s.NodeNames = ss(nodes[rng.Intn(len(nodes))])
					}
					if rng.Intn(6) == 0 {
						s.ReservedClaim = rndName("c", 10)
					}
					err := c.UpdateVolume(name, s)
					found, ok := m.updateVolume(name, s)
					log.step(itoa(int64(step))+" UpdateVolume "+name, err,
						"对照朴素 found="+boolStr(found)+" ok="+boolStr(ok))
					requireSame(step, "UpdateVolume", err == nil, err, found && ok)
				case 4:
					name := rndName("v", 14)
					err := c.ResetVolume(name)
					found, ok := m.resetVolume(name)
					log.step(itoa(int64(step))+" ResetVolume "+name, err,
						"对照朴素 found="+boolStr(found)+" ok="+boolStr(ok))
					requireSame(step, "ResetVolume", err == nil, err, found && ok)
				case 5:
					name := rndName("c", 14)
					newCap := int64(1 + rng.Intn(25))
					err := c.ExpandClaim(name, newCap)
					found, ok := m.expand(name, newCap)
					log.step(itoa(int64(step))+" ExpandClaim "+name+" -> "+itoa(newCap),
						err, "对照朴素 found="+boolStr(found)+" ok="+boolStr(ok))
					requireSame(step, "ExpandClaim", err == nil, err, found && ok)
				case 6:
					// 对 1~3 个已存在的延迟待绑定声明发起联合绑定。
					var pool []string
					for n, cs := range m.claims {
						if !m.cbound[n] && cs.BindMode == BindWaitForConsumer {
							pool = append(pool, n)
						}
					}
					if len(pool) == 0 {
						continue
					}
					rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
					k := 1 + rng.Intn(min(3, len(pool)))
					list := append([]string(nil), pool[:k]...)
					node := nodes[rng.Intn(len(nodes))]
					res, err := c.JointBind(JointBindRequest{Node: node, ClaimNames: list})
					naiveRes, naiveOK := m.joint(node, list)
					log.step(itoa(int64(step))+" JointBind node="+node+" claims="+
						joinNames(list), resultOrErr(res, err),
						"对照朴素 ok="+boolStr(naiveOK))
					requireSame(step, "JointBind", err == nil, err, naiveOK)
					if err == nil && len(res) != len(naiveRes) {
						t.Fatalf("seed=%d joint assignment length differs", seed)
					}
					if err == nil {
						for _, a := range res {
							if naiveRes[a.ClaimName] != a.VolumeName {
								t.Fatalf("seed=%d step=%d joint assignment differs: %s -> %s vs %s",
									seed, step, a.ClaimName, a.VolumeName, naiveRes[a.ClaimName])
							}
						}
					}
				default:
					// 空闲步：只做一次自检与指纹对照。
					requireSame(step, "noop", true, nil, true)
				}
			}
			log.step("最终状态指纹", nil, "\n"+controllerFingerprint(c.Snapshot()))
		})
	}
}

func joinNames(list []string) string {
	out := "["
	for i, n := range list {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out + "]"
}

func resultOrErr(res []Assignment, err error) interface{} {
	if err != nil {
		return err
	}
	return res
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

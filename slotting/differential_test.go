package slotting

import (
	"math/rand"
	"testing"
)

// TestRandomDifferential 与独立朴素模型对照大量随机操作序列。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		t.Run("seed-"+itoa(int(seed)), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			s := NewSystem(nil)
			m := newNaive()

			catsAll := []Category{CatGeneral, CatFood, CatFlammable}
			nLocs := 30 + rng.Intn(40)
			locList := make([]LocationID, 0, nLocs)
			for k := 0; k < nLocs; k++ {
				id := LocationID{rng.Intn(4) + 1, rng.Intn(3) + 1, rng.Intn(12) + 1}
				if _, dup := s.st.locations[id]; dup {
					continue
				}
				capacity := 1 + rng.Intn(2)
				allowed := []Category{}
				for _, c := range catsAll {
					if rng.Intn(2) == 0 {
						allowed = append(allowed, c)
					}
				}
				if len(allowed) == 0 {
					allowed = []Category{catsAll[rng.Intn(3)]}
				}
				loc := Location{
					ID:          id,
					MaxWeight:   20 + rng.Intn(480),
					ClearHeight: 20 + rng.Intn(280),
					Allowed:     allowed,
					Capacity:    capacity,
					AllowMix:    rng.Intn(2) == 0,
					Status:      StatusNormal,
				}
				if err := s.AddLocation(loc); err != nil {
					t.Fatalf("AddLocation: %v", err)
				}
				m.add(loc)
				locList = append(locList, id)
			}

			register := func() string {
				id := "pal" + itoa(len(m.pallets))
				p := Pallet{
					ID:       id,
					Product:  []string{"A", "B", "C", "D"}[rng.Intn(4)],
					Batch:    "b" + itoa(rng.Intn(4)),
					Category: catsAll[rng.Intn(3)],
					Weight:   1 + rng.Intn(200),
					Height:   1 + rng.Intn(150),
				}
				if err := s.RegisterPallet(p); err != nil {
					t.Fatalf("register: %v", err)
				}
				m.reg(p)
				return id
			}
			for i := 0; i < 40; i++ {
				register()
			}

			palletIDs := func() []string {
				out := make([]string, 0, len(m.pallets))
				for id := range m.pallets {
					out = append(out, id)
				}
				return out
			}

			for step := 0; step < 800; step++ {
				switch rng.Intn(7) {
				case 0:
					pid := register()
					gotID, gotErr := s.AutoPlace(pid)
					wantID, wantOK := m.auto(pid)
					if (gotErr == nil) != wantOK {
						for dpid, did := range m.where {
							sid, _ := s.PalletLocation(dpid)
							if sid != did {
								t.Errorf("此前分歧: %s sys=%s naive=%s", dpid, sid, did)
							}
						}
						t.Fatalf("step %d auto %s 分歧: sys=%v naiveOK=%v", step, pid, gotErr, wantOK)
					}
					if gotErr == nil && gotID != wantID {
						t.Fatalf("step %d auto %s 选位分歧: sys=%s naive=%s", step, pid, gotID, wantID)
					}
				case 1, 2:
					pids := palletIDs()
					pid := pids[rng.Intn(len(pids))]
					target := locList[rng.Intn(len(locList))]
					sysErr := reasonOf(s.PlaceTo(pid, target))
					want := m.placeTo(pid, target)
					if sysErr != want {
						t.Fatalf("step %d placeTo %s->%s 分歧: sys=%s naive=%s",
							step, pid, target, sysErr, want)
					}
				case 3:
					pids := palletIDs()
					pid := pids[rng.Intn(len(pids))]
					target := locList[rng.Intn(len(locList))]
					sysErr := reasonOf(s.Move(pid, target))
					want := m.move(pid, target)
					if sysErr != want {
						t.Fatalf("step %d move %s->%s 分歧: sys=%s naive=%s",
							step, pid, target, sysErr, want)
					}
				case 4:
					pids := palletIDs()
					pid := pids[rng.Intn(len(pids))]
					_, takeErr := s.TakeOut(pid)
					sysOK := takeErr == nil
					wantOK := m.take(pid)
					// TakeOut 对“已取出”返回错误；朴素模型同样 false。
					if sysOK != wantOK {
						t.Fatalf("step %d take %s 分歧 sys=%v naive=%v", step, pid, sysOK, wantOK)
					}
				case 5:
					id := locList[rng.Intn(len(locList))]
					frozen := rng.Intn(2) == 0
					sysErr := reasonOf(s.Freeze(id))
					if !frozen {
						sysErr = reasonOf(s.Unfreeze(id))
					}
					mOK := m.freeze(id, frozen)
					if (sysErr == "") != mOK {
						t.Fatalf("step %d freeze %s 分歧", step, id)
					}
				case 6:
					// 批量 1-4 个托盘（可能含重复/已上架，朴素模型逐个处理）。
					k := 1 + rng.Intn(4)
					pick := []string{}
					for j := 0; j < k; j++ {
						pids := palletIDs()
						pick = append(pick, pids[rng.Intn(len(pids))])
					}
					idx, err := s.BatchAutoPlace(append([]string(nil), pick...))
					c := cloneNaive(m)
					// 与系统完全一致的前置校验：最小重复下标（参数非法）。
					firstSeen := map[string]int{}
					naiveIdx, naiveReason := -1, Reason("")
					for j, pid := range pick {
						if first, ok := firstSeen[pid]; ok {
							_ = first
							naiveIdx, naiveReason = j, ReasonInvalidArgument
							break
						}
						firstSeen[pid] = j
					}
					if naiveIdx == -1 {
						for j, pid := range pick {
							if _, on := c.where[pid]; on {
								naiveIdx = j
								naiveReason = ReasonDuplicate
								break
							}
							if wid, ok := c.auto(pid); !ok {
								naiveIdx = j
								_ = wid
								naiveReason = ReasonNoAvailableLocation
								break
							}
						}
					}
					if err == nil {
						if naiveIdx != -1 {
							t.Fatalf("step %d 批量系统成功但朴素在 %d 失败(%s)", step, naiveIdx, naiveReason)
						}
						m = c // 提交朴素结果
					} else {
						if naiveIdx != idx {
							t.Fatalf("step %d 批量失败下标分歧: sys=%d(%s) naive=%d(%s)",
								step, idx, reasonOf(err), naiveIdx, naiveReason)
						}
					}
				}
				assertSnapshotsEqual(t, s, m)
			}
		})
	}
}

func cloneNaive(m *naiveModel) *naiveModel {
	c := newNaive()
	for id, ls := range m.locs {
		c.add(ls.loc)
		c.locs[id].pallets = append([]string(nil), ls.pallets...)
		c.locs[id].weight = ls.weight
	}
	for pid, id := range m.where {
		c.where[pid] = id
	}
	for _, p := range m.pallets {
		cp := *p
		c.reg(cp)
	}
	return c
}

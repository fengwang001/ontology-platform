// Package pivas 是医院静脉用药配置中心（PIVAS）的排程与稳定期判定内核。
//
// 典型用法：
//
//	s := pivas.New()
//	_ = s.SetTransport(0, pivas.Room, 600)       // 室温运送 10 分钟
//	_ = s.SetTransport(0, pivas.Cold, 900)       // 冷藏运送 15 分钟
//	_ = s.SetDuration(0, 1, 300)                 // 1 袋配置 5 分钟
//	_ = s.SetDuration(0, 2, 420)                 // 2 袋配置 7 分钟
//	_ = s.RegisterBench(0, pivas.Bench{ID: "A", Capacity: 4, ClearGap: 60})
//	_ = s.RegisterDrug(0, "头孢", pivas.Drug{
//	    RoomStableSec: 7200, ColdStableSec: 86400,
//	    SolventClass: "NS",
//	})
//	err := s.AcceptOrder(100, pivas.OrderInput{
//	    ID: "ORD-1", Drugs: []string{"头孢"}, Solvent: "NS",
//	    DueAt: 10000, Urgent: false,
//	})
//	if err != nil { /* 按 err.(*pivas.Error).Code 分支 */ }
//	info, _ := s.QueryOrder(100, "ORD-1")

package pivas

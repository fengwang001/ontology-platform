package planningpoker

// Package planningpoker 提供团队估点（Scrum Poker）投票会话服务。
//
// 快速上手：
//
//	s, err := planningpoker.NewSession("alice", planningpoker.Config{
//	    Cards:        []int{1, 2, 3, 5, 8, 13},
//	    RoundLimit:   3,
//	    RoundSeconds: 120,
//	    AutoReveal:   true,
//	}, 0)
//
//	s.Join("bob", planningpoker.RoleVoter, 1)
//	s.Start("alice", 2)
//	s.Vote("bob", planningpoker.Card{Value: 5}, 3)
//
//	r, err := s.Reveal("alice", 4) // 或到期/自动揭示
//	// r.Category, r.Value, r.Distribution, r.NumericVotes, r.Final
//
// 时间戳 now 为非负整数秒，必须单调不减；所有入口在同一把锁下串行执行。
// 两张特殊牌为 CardUncertain（不确定）与 CardBreak（需要休息），
// 计入“是否有人投票”，但不参与揭示后的数值统计。

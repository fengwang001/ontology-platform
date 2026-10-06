// Package pathlock 实现仓库级大文件路径锁服务。
//
// 协作者推送二进制文件前先对规范化路径加锁；服务支持锁的创建、查询、
// 释放与管理员强制释放，并在推送校验阶段对一批路径做全有或全无的持锁
// 核验。锁在祖先与后代方向上对他人排他，对本人放行。
//
// 典型用法：
//
//	s := pathlock.NewService()
//	l, _ := s.Acquire("alice", "assets/model.bin")
//	res, _ := s.Verify("alice", []string{"assets/model.bin"}, true)
//	_ = res.Allowed
//	_, _ = s.Release("alice", l.ID, false)
//
// 关键语义、被放弃方案与复杂度论证见 DESIGN.md。
package pathlock

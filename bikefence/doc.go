// Package bikefence 实现共享单车电子围栏的还车判定与跨围栏调度。
//
// 核心入口：
//   - [New] / [NewNaive]：创建主服务（R 树索引）与朴素参照服务（逐围栏判定）。
//   - [Service.RegisterFence]：登记运营区/禁停区/奖励区并校验空间约束。
//   - [Service.ReturnBike] / [Service.Unlock]：还车（容量、禁停、区外费、奖励、疏散）
//     与解锁释放名额。
//   - [Service.ClaimTask] / [Service.CompleteTask]：调度任务认领、超时释放与落点校验。
//   - [Service.Locate]：归属查询，返回判定依据与 R 树访问节点数（复杂度证据）。
//   - [Service.FenceCount]：O(1) 围栏当前车辆数。
//
// 所有写操作携带单调时刻，被拒操作不改变任何状态；错误通过 [Error] 的 Kind 字段区分。
// 设计取舍与本地验证见同目录 DESIGN.md。
package bikefence

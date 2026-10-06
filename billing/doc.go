// Package billing 实现按带宽采样计费的单周期结算器。
//
// 一个计费周期由起始时刻与 N 个固定 300 秒槽位组成。每个槽位至多保留
// 一份带版本号的入向/出向采样；有效值取入向、出向的较大者。计费速率
// 定义为：有效槽位有效值降序排列，丢弃 floor(5%*K) 个最大值后的剩余
// 最大值（K=0 时无定义）。
//
// 核心类型 [Biller] 的所有方法可并发调用；提交、撤回、查询、概览均为
// 期望 O(log K)（外加槽位数组的 O(1) 访问），与已收到采样总数无关。
// 结算成功后周期封存，重复结算恒返回首次结果并标记 Repeated。
//
// 错误以 [ErrorCode] 字符串区分，判定优先级固定：
//
//	invalid_argument > already_settled >
//	(version_conflict / stale_sample / version_mismatch) >
//	not_found > (no_samples / insufficient_data)
//
// 同包的 [NaiveBiller] 是按定义直写的独立参考实现（每次查询全量排序），
// 仅供测试对照。
package billing

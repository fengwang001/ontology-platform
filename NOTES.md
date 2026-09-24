# NOTES：有界重排窗口的保序并行映射器

## 第三节推导：多个输入失败时返回哪个错误
- 约束：返回值须与失败时序无关（第二节第 3 条），且与顺序参照一致
  （第二节第 1 条）。`check.Naive` 按下标顺序执行、遇错即停，报告的
  必然是「下标最小」的失败；5 先失败、3 后失败时，顺序语义下 3 才是
  失败点，emit 前缀只能是 0,1,2。
- 若返回「时间上最早到达」的错误（下标 5），返回值随调度时序变化，
  且与同一输入的顺序参照结果不一致——这正是线上事故根因。
- 结论：返回「失败下标最小」的错误；emit 恰好输出该下标之前的结果。
- 推论 1：收到第一个错误后不能立刻停止等待——下标更小、仍在跑的输入
  随后可能失败，须等全部在跑的 fn 退出才能确定最小失败下标。
- 推论 2：内部取消传播后，在跑的 fn 返回的 ctx.Err() 是「伪失败」，
  不计入失败下标（否则返回值又变得依赖时序）；见 pmap/pmap.go:62。
- 钉住：TestFailDeterministic 让 5 先失败、3 后失败，200 次都返回 3
  的错误且 emit 恰为 0,1,2；同测试内联「返回最先到达错误」的错误
  实现作对照，它返回 5。

## 语义落点（第二节各条 → 代码位置 / 测试函数）
1. 保序：window.Put/Ready/Pop + pmap.Run 的 emit 循环；TestOrderAndWindow（对照 check.Naive）。
2. 上界：window.Admit（window/window.go:17）限窗、pmap.go:46 的 inflight<n 限并发；check.run 断言峰值，TestOrderAndWindow/TestResources。
3. 失败语义：pmap.Run 的 minErr 归约 + cancel + 排干（pmap/pmap.go:57-68）；TestFailDeterministic。
4. 错误：ErrBadConfig（pmap.go:16）、%w 包装（pmap.go:88）、parent.Err()（pmap.go:81-85）；TestErrorsAndCancel。
5. 资源：Run 收干 res 通道且 wg.Wait 后才返回（pmap.go:39、76-80）；TestResources 断言 goroutine 回基线。

## 第四节实测
- n=8、w=4、10000 条随机耗时输入：历史最大暂存数实测 = 4（≤4 成立），见 TestResources 日志。

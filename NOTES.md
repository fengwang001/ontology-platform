# Fisher-Yates 均匀洗牌推导与索引

## 推导：为什么必须从 [i, n) 选

把洗牌看成一棵决策树：每一步选哪个下标交换，决定最终排列。

- 正确做法（Fisher-Yates，从前往后）：第 i 步在 `[i, n)` 中均匀选一个下标
  与位置 i 交换。第 i 步有 n-i 种选择，总路径数 = n·(n-1)·…·1 = n!，
  每条路径概率 1/n!。且路径与排列一一对应（给定路径可唯一还原排列，
  反之每个排列恰由一条路径产生），故每种排列概率恰好 1/n!，均匀。
- 错误做法（每步从 `[0, n)` 全范围选）：n 步每步 n 种选择，总路径数 n^n，
  每条路径概率 1/n^n。但排列只有 n! 种，而 n! 不能整除 n^n
  （如 n=3：27 条路径对 6 种排列，27/6 非整数），由鸽巢原理，某些排列
  对应的路径数必然更多、某些更少（甚至为零），概率不可能均匀。

测试钉住：n=4 洗 120000 次统计 24 种排列计数，正确实现 max/min ≤ 1.5；
内联全范围错误实现 max/min > 5。

## 语义条目位置

- 确定性可复现：shuffle/shuffle.go `Shuffle`（种子入 rng.New）；测试 `TestShuffleSemantics`
- 无丢失（多集置换）：同上测试中对排序后结果与原数组比较
- 均匀性 n=4×120000：check/check.go `CountPermutations`/`VerifyUniform`；测试 `TestUniformity`（24 种，max/min ≤ 1.5）
- 空/单元素合法：`Shuffle` 中循环不执行原样返回；测试 `TestShuffleSemantics`（n=0/1 用例）
- n=2 各约 50%：测试 `TestUniformity` 的 "n=2 各半" 用例（120000 次，max/min ≤ 1.2）
- 错误实现对照：check/check_test.go `badShuffle`（全范围选）；`TestUniformity` 的 "全范围错误实现" 用例（max/min > 5）
- 随机调用恰 n-1 次：shuffle/shuffle.go 非导出计数器 `randCalls`；测试 `TestShuffleSemantics`
- 哨兵错误：`rng.ErrNonPositiveN`、`shuffle.ErrNilSlice`、`check.ErrNonUniform`，均可用 errors.Is 区分；测试 `TestSentinelErrors`
- 并发安全：每次 Shuffle 独立 rng 状态，计数器用 atomic；测试 `TestConcurrentDistinctArrays`（-race 干净）

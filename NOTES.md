# NOTES

## 八步推导（nPart=2，元素 a,b；起始 cnt[a]=cnt[b]=0，View={}）

| 步 | 操作 | cnt[a] | cnt[b] | 本步 changelog | 本步后 View |
| 1 | Add(0,"a") | 1 | 0 | +a | {a} |
| 2 | Add(1,"a") | 2 | 0 | 无 | {a} |
| 3 | Add(0,"b") | 2 | 1 | +b | {a,b} |
| 4 | Remove(0,"a") | 1 | 1 | 无 | {a,b} |
| 5 | Remove(1,"a") | 0 | 1 | -a | {b} |
| 6 | Remove(0,"b") | 0 | 0 | -b | {} |
| 7 | Add(1,"b") | 0 | 1 | +b | {b} |
| 8 | Remove(0,"b") | 0 | 1 | 无（幂等） | {b} |

- 甲：第 4 步 cnt[a]=2，**不输出**。错版（任一 Remove 即撤回）会输出 `-a`，View 错成 {b}（a 仍被分区 1 持有）；第 5 步又会额外错输出一条 `-a`（重复撤回已不在视图的元素）。
- 乙：第 8 步 "b" **不在分区 0**（第 6 步已移除），不输出。错版（无条件转发撤回）会输出 `-b`，View 错成 {}；实际 b 仍被分区 1 持有，应仍为 {b}。
- 丙：第 2 步 "a" 已在视图（cnt=1）。错版（每次 Add 都发 +）会再输出 `+a`；下游不隔 `-` 连收两个 +a，把 "a" 数成 2 份。

## 四条不变量：代码保证位置 / 钉住的测试函数

1. View==批量重算并集：`uni.(*Engine).Add/Remove` 仅在 cnt 0↔1 跳变时改变视图归属，`View()` 取 cnt>=1 —— `api_test.go: TestViewMatchesUnion`（随机序列对拍独立模型）。
2. changelog 自洽：仅 0→1 发 `+`、1→0 发 `-`，天然严格交替 —— `api_test.go: TestChangelogAlternation`（逐前缀重放校验）。
3. 引用计数守恒：cnt 只随分区集合的真实插入/删除各 ±1 —— `api_test.go: TestRefCountConservation`（随机序列逐元素核对分区数）。
4. 失败不留痕：`api.check` 先校验后变更、`Apply` 整批先验后写 —— `api_test.go: TestRejectedOpsLeaveNoTrace`；三类互异哨兵 `TestSentinelErrors`。

## 复杂度（第四节）

`uni.Engine.lastProbe`（非导出）记录最近一次 Remove 检查的分区数；实现用全局 cnt map O(1) 判定，恒为 0。钉住：`uni_test.go: TestProbeConstantInM`（m=100..10000 多档，含最后一次 cnt 1→0 的 Remove）；demo 经 `uni.ProbeBounded` 只取布尔结论，计数器数值无导出通道。

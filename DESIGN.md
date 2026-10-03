# 令牌式流程引擎设计（OR 汇合 + 定义版本固定）

## 包划分
- `model`：图结构（`Graph`：`N`、`Kinds`、`Edges`，节点编号 1..N，N≤64）、结构校验、边/邻接辅助、错误哨兵。
- `repo`：`Define` 保存不可变版本（版本号从 1 起，被拒不占号）；`Start(inst, defID, choices)` 固定当时最新版本快照，之后新版本不影响在途实例。
- `engine`：`Complete(inst, t)` 按有效图驱动令牌；`Status(inst)` 只读返回 Running/Completed/Stuck、endCount、各汇合触发次数。

## 有效图（裁剪图）与可达性
- Start 时按 choices 裁剪：XorSplit 保留选中边；OrSplit 保留选中子集；其余边全部保留。
- 在有效图上计算传递闭包（uint64 位图，N≤64），Start 一次算好。
- OrJoin 放行条件：`sum(arr)>=1` 且不存在"其它令牌所在节点 p"在有效图上可达该 OrJoin。
  "其它令牌"= `act[p]>0` 的 Task 或 `sum(arr[p])>0` 的其他汇合；探测点计数与有令牌节点数同阶，与 n 无关（非导出计数，测试按 n=10/64 两档核对）。

## 取舍
- 采用选择已知的有效图做保守结构可达判定：不做任何状态枚举，永不提前（错误）放行；
  代价是滞留在其他汇合的令牌可能让本可放行的 OrJoin 多等一轮（令牌流动后下一轮即放行），
  最终状态与触发次数不变。
- 放弃方案 A：穷举令牌可达状态空间做精确判定——状态数指数增长，不可实现/不可复现的性能。
- 放弃方案 B：在未裁剪的静态结构图上判定——被 choices 裁掉的分支仍被当作"可能到达"，
  会无谓阻塞 OrJoin（如题干例子 Complete(4) 时 3→5→7 已被裁掉，仍被挡住），触发时机错误。
- OrJoin 一次触发把该点全部 arr 清零、只产出 1 个令牌：一次触发合并此前全部已到达令牌，
  而非逐令牌触发；因此"触发次数"有确定语义，且与完成顺序无关。

## 执行循环（每次 Complete 后）
1. 校验 Task 且 act>0，否则 ErrNoActive；act--，向其出边放令牌。
2. 落点分发：Task→act++；分叉→沿（选定）出边放令牌；End→endCount++；汇合→对应入边 arr++。
3. 按节点编号升序反复扫描所有汇合直到无变化：
   AndJoin 所有入边 arr≥1 则各减 1 放 1 令牌；OrJoin 满足上述条件则全部清零放 1 令牌。
   新放出的令牌在下一轮继续参与（汇合链也在一轮 Complete 内收敛）。
4. 状态：有 act>0 → Running；无 act 且 arr 全 0 → Completed；否则 Stuck（如 AndJoin 等不到被裁分支）。
5. 每个实例一把互斥锁；Complete 并发等价于某一串行顺序，全排列重放终态一致。

## 错误优先级（errors.Is 可区分，被拒操作不改状态）
空 inst/defID 等参数非法 → 定义不存在 → 实例已存在/不存在 → Define：ErrStructure、ErrCycle、ErrReach；
Start：ErrChoice（缺失/越界/重复/多余）；Complete：ErrNoActive。

## 本地验证
- `go test ./...`、`go test -race ./...`、`go vet ./...`、`gofmt -l .`
- 随机图测试对拍：每步在有效图上现算可达的逐步朴素模拟，且任务数≤5 时遍历全部完成顺序核对终态一致；打印输入/输出/判定依据日志（`go test -v`）。

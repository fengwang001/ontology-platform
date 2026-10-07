# 校验钩子的注册、排序与短路执行机制设计

包路径：`ontology/validate`（泛型，可分别用于对象类型与链接类型）。

## 1. 要解决的问题

为对象类型 / 链接类型挂载多个校验钩子，并使下列行为**确定且可预期**：

1. 钩子按“优先级分组 + 组内注册顺序”执行；
2. 组间总是短路；组内是否短路由分组声明决定；
3. 每次调用使用调用开始时刻的快照，调用期间的注册/注销不影响本次调用；
4. 注销后以同 ID 重新注册视为新注册，排到组末；
5. 同一批次注册保留批次内声明的相对顺序；
6. 被短路跳过的低优先级钩子不产生“看似执行过”的痕迹，只留明确的“未执行”审计记录；
7. 区分三种分组结果：短路拒绝 / 汇总拒绝 / 钩子异常不可判定；异常单独上抛；
8. “跳过了多少钩子”只与本次快照大小相关，与历史注册/注销总量无关；
9. 并发交织下每次调用等价于某个全序串行历史中的一次调用。

## 2. 核心模型

### 2.1 分组与排序

- `GroupSpec{Name, Priority, ShortCircuit}` 声明分组。
- 组间次序：`Priority` 降序；同优先级按 `Name` 字典序兜底（不依赖 map 随机序）。
- 分组必须先声明（`NewRegistry` / `DeclareGroup`），钩子引用未声明分组直接报错。

### 2.2 组内顺序

- 单个 `Register`：追加到所属分组末尾。
- `RegisterBatch`：作为一个对外原子动作；逐条按**批次内声明顺序**追加到各自分组。
  所有条目同一次状态发布生效，因此不存在“按批次提交时刻重排”。
- 钩子 ID 全局唯一：重复 ID（含批次内部自重复）报错，且批次整体不生效（原子）。

### 2.3 不可变快照（copy-on-write）

`Registry` 内部保存一个不可变 `state`：

- 所有变更在一把 `sync.Mutex` 临界区内：拷贝旧状态 → 修改副本 → 赋值发布。
- `Snapshot()` / `Validate()` 在同一把锁的临界区内读取当前 `state` 指针，
  并把内容**值拷贝**成独立的 `Snapshot` 返回后立即释放锁。
- 快照中只保存当前存活钩子，**没有墓碑（tombstone）**。注销直接重建该组切片，
  旧版本快照引用旧底层数组，天然不受影响；重新注册必然追加到“当前”末尾。

线性化点：变更在临界区内的赋值点，读在临界区内的读取点。因此所有操作存在一个
全序，`Validate` 看到的快照必为该全序中某个已存在时刻。

### 2.4 执行语义

设组序 G1 > G2 > …，组间状态 `blockedGroup`、`errored`：

- 若更高优先级组已拒绝或已异常：当前组所有钩子标记为
  `skipped_group_blocked`，**钩子函数绝不被调用**。
- 否则逐钩执行：
  - 通过 `run_approved`；
  - 拒绝 `run_rejected`：
    - 该组 `ShortCircuit=true`：组内后续钩子标记 `skipped_short_circuit`，停止本组；
    - `ShortCircuit=false`：继续执行组内全部钩子，跑完再汇总
      （分组结果 `rejected_aggregated`）；
    - 无论哪种，都置组间阻断，低优先级组不执行。
  - 钩子返回 error 或 panic：`run_error`，封装为 `*HookExecutionError`，
    组内后续钩子标记 `skipped_error`，低优先级组标记 `skipped_group_blocked`，
    最终把错误**单独上抛**。
- 非短路组中“先拒绝、后异常”：以异常为准——分组结果 `indeterminate_error`，
  `Validate` 返回错误，`FinalDecision` 不赋予“拒绝”含义。

`panic` 在 `runHook` 内 `recover`，与返回 error 走同一“不可判定”通道，
不会逃逸为进程崩溃。

### 2.5 审计与“不可观察”的边界

`Report` 覆盖快照中**每个分组、每个钩子**，但状态严格区分：

- 真正执行：`run_approved` / `run_rejected` / `run_error`（带 Outcome）；
- 未执行：`skipped_short_circuit` / `skipped_error` / `skipped_group_blocked`
  （Outcome 为零值、Err 为 nil）。

这样低优先级钩子被跳过时，统计/日志路径不可能出现“它执行过”的假象，
同时审计仍能区分“通过 / 拒绝 / 因何未执行”。

分组结果枚举：`approved`、`rejected_short_circuit`、`rejected_aggregated`、
`indeterminate_error`、`skipped_blocked`，三类关键结论分别暴露。

`Basis` 给出最终判定依据（短路首个拒绝点 / 汇总拒绝的全部拒绝钩子 / 异常点）。

### 2.6 跳过计数的复杂度

`Report.SkippedHooks()` 是在执行本次快照时维护的三个计数器之和，
计数器只在遍历**本次快照**的钩子时增减：

- 时间复杂度 O(S)，S = 本次快照存活钩子数；
- 因为状态里不保存墓碑，空间上也没有任何随历史注册/注销总量 H 增长的结构；
- `SkippedHooks() + RanHooks()` 恒等于快照钩子总数（测试断言）。

`TestSkippedCountIndependentOfHistory` 用两个终态快照相同、但其中一个额外
制造 5000 次注册+注销“噪声”的注册表对照，验证跳过数与结论完全一致；
基准 `BenchmarkValidateApproved` 展示耗时/分配随 S 线性、与 H 无关。

## 3. 关键取舍

- **互斥锁 + copy-on-write，而非无锁 CAS**：钩子数量小、读多写也不少；
  锁内只做指针读取/结构拷贝发布，临界区极短。COW 让“快照隔离”成为数据结构
  的自然属性，而不是给每次执行加长锁。无锁版本（atomic.Pointer + CAS 重试）
  被放弃：批次与多 map 更新的 CAS 组合复杂、易错，收益却不明显。
- **快照返回值拷贝而非共享内部切片**：彻底杜绝“调用进行中后台修改污染快照”，
  也使“同一次调用前后两次读取一致”无需额外同步。
- **没有墓碑**：墓碑会让跳过统计/内存随历史增长，直接违背第 8 条；注销即物理删除。
- **异常优先级高于拒绝**：非短路组里可能同时观察到拒绝与异常；把异常单独上抛、
  分组判为不可判定，避免把“系统故障”静默归并成“业务拒绝”。
- **顺序执行而非并行执行钩子**：钩子可能有副作用且要求注册顺序可预期；
  并行会引入数据竞争与结论不确定性。短路也只有在顺序语义下才有清晰定义。
- **分组必须显式声明**：避免“拼错分组名悄悄新建一个优先级未知的组”。

## 4. 被放弃的方案

- 全局单一优先级序号（扁平、无分组）：无法表达“组内必须全部跑完再汇总”。
- 钩子返回 bool 表达通过/拒绝：无法区分“拒绝”与“钩子自身出错”，故使用
  `(Outcome, error)`。
- 在日志里为被跳过钩子写一条普通“执行”记录：违背不可观察性要求，改为显式
  `skipped_*` 状态。
- 读操作复用活动切片（零拷贝）：已被并发线性化测试证伪（就地过滤会污染旧快照，
  见测试中对朴素模型 `old[:0]` 反例的说明）。

## 5. 公共 API 概览

```go
reg, _ := validate.NewRegistry[*ObjectType](
    validate.GroupSpec{Name: "structural", Priority: 100, ShortCircuit: true},
    validate.GroupSpec{Name: "semantic",   Priority: 10,  ShortCircuit: false},
)

reg.Register(validate.Hook[*ObjectType]{ID, Group, Fn})
reg.RegisterBatch(validate.Registration[*ObjectType]{Hook: h1}, /*...*/)
reg.Unregister(id)            // bool：注销前是否存在
reg.DeclareGroup(spec)        // 运行期追加分组

snap := reg.Snapshot()        // 独立不可变快照
rep, err := reg.Validate(ctx, target)
// rep.FinalDecision / rep.Basis / rep.Groups / rep.SkippedHooks() ...
// err 非 nil 时为 *validate.HookExecutionError
```

## 6. 本地验证方法

需要 Go 1.26+（仓库环境若 `go` 不在 PATH，使用 `/usr/local/go/bin`；
若 `GOCACHE` 位于只读分区，指向可写目录）：

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache

go test ./...                                   # 全量
go test -race ./...                             # 竞态检测
go test ./ontology/validate -run TestConcurrentLinearizationAgainstNaive -count=30
go test ./ontology/validate -run TestGroupCombinations -v
go test ./ontology/validate -run TestSkippedCountIndependentOfHistory -v
go test ./ontology/validate -bench BenchmarkValidateApproved -run XXX
go vet ./... && gofmt -l .
```

### 并发线性化对照测试如何工作

`linearization_test.go` 内有一个**独立手写**的朴素串行参考模型 `naiveModel`
（一把锁、就地状态、每个全序版本冻结一份结构化快照），它不共享生产调度代码：

- 一个 `linearizer` 互斥区把“对真实注册表的一次变更”和“同一操作在朴素模型上的
  重放”绑定为同一个全序点；
- `Validate` 在锁外与变更真正并发执行；
- 每次调用结束后，用它读到的快照版本号取该版本的朴素冻结快照，在其上推演
  预期的执行序列、跳过数、最终结论，与真实 `Report` 逐项对照；
- 并断言：快照版本不超过全序前沿、终态存活钩子序列一致、每次观察到的快照都能
  对应到全序中的某个时刻。`-race` 下反复运行无数据竞争。

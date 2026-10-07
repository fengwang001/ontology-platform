# 跨对象类型权限传播模块 — 设计说明

## 总览

模块位于 `ontology/` 包，核心由四部分组成：

- `Store`（`ontology/store.go`）：已提交状态（对象类型、链接类型、实例、链接、审计、逻辑时钟）与事务暂存区 `tx`。
- `Engine`（`ontology/engine.go`）：动作执行引擎，负责参数校验、传播遍历、权限检查、合并裁决与原子提交。
- `Authorizer`（`ontology/auth.go`）：权限判定接口，引擎对每次判定计数（`CountingAuthorizer` / 内部 `checkCounter`）。
- 朴素参照实现（`ontology/reference.go`）：与引擎不共享代码的独立实现，用于差分测试。

一次 `Engine.Execute` 按六个阶段推进，任一阶段失败即整体拒绝：

1. 参数校验（`ErrInvalidParams`）
2. 结构遍历与深度上限检查（`ErrDepthExceeded`）
3. 直接目标可见性（`ErrTargetInvisible`）
4. 级联实例可见性（`ErrCascadeInvisible` 或跳过）
5. 多层级授权结论合并（拒绝非错误，返回 `allowed=false`）
6. 暂存区原子提交（实例、审计、时钟同生共死）

阶段顺序即错误汇报的固定优先顺序，编号越小越优先（`ontology/errors.go`）。

## 关键取舍

### 严格深度语义（而非静默截断）

级联规则声明 `MaxDepth` 后，若实际关系图要求更深的传播才能覆盖全部级联影响
（即最深 frontier 仍存在通向未访问实例的边），引擎以 `ErrDepthExceeded` 拒绝，
而不是静默截断。理由：声明的深度上限是对外承诺的影响边界，静默截断会让
“声明的影响范围”与“实际的影响范围”悄悄分叉，且使 `ErrDepthExceeded`
这一错误类别永远无法触发。代价是声明者必须给出足以覆盖真实图的上限；
环上的回边不算越界（只统计通向未访问实例的边）。

### 合并规则用可交换、可结合的折叠

`MergeAll` 是逻辑与、`MergeAny` 是逻辑或。两者都是可交换、可结合的折叠，
因此合并结果天然与传播过程中访问各层级的顺序无关——这不是靠测试保证的
性质，而是由代数结构保证的（测试 `TestMergeOrderIndependence` 只是回归保护）。
被放弃的方案：按“最严格层级优先”或“最深层级优先”的有序合并——它们都
引入了对遍历顺序的敏感性，且语义难以向动作声明者解释。

### 单互斥锁串行化（而非 MVCC/细粒度锁）

`Engine` 用一把 `sync.Mutex` 串行化整个 `Execute`，因此可串行化是构造即得的：
任意并发调用的结果等价于按锁获取顺序的串行执行，被拒绝的动作在暂存区被
丢弃，对后续动作完全不可见。被放弃的方案：MVCC 快照 + 提交时冲突检测
（乐观并发）。它能提高吞吐，但需要定义并验证冲突粒度（实例级？链接级？），
且“被拒绝动作不产生任何可观察影响”在乐观重试下更难证明。当前模块以
正确性为先；锁是唯一的串行点，吞吐瓶颈清晰可控。

### 事务暂存区（而非就地修改 + 补偿回滚）

所有写入先落在 `tx` 暂存区（创建/修改的新实例状态 + 审计记录），最终判定
为允许时由 `Store.commit` 一次性落盘并推进时钟。被放弃的方案：就地修改 +
失败时补偿回滚——补偿逻辑在部分失败（如中途 panic）下难以保证完备，
而暂存区的丢弃是零成本的、显然正确的。

### 跳过记录只承载“存在且不可见”

`SkipRecord` 只含实例 ID、原因常量与影响范围常量字符串，不含任何属性值；
`ErrCascadeInvisible` 不携带实例标识。ID 本身泄露“存在且不可见”，这是
跳过模式显式接受的语义；其余信息一律不进入日志与错误。

### 有向遍历

传播只沿链接类型的声明方向（From→To）。被放弃的方案：双向遍历——它会让
“沿某些链接类型不传播”的排除声明产生歧义（排除的是边还是方向？），且
放大级联影响面，不利于深度上限的推理。

## 权限检查次数的可观测证明

引擎在单次执行内用 `checkCounter` 统计 `Visible`/`Authorize` 调用次数并写入
判定日志（`DecisionRecord.CheckCount`）。上界为 `2 × (|直接目标| + |级联触及实例|)`，
只与动作实际声明与实际触及的规模相关。`TestCheckCountIndependentOfSystemSize`
通过注入 2000 个无关类型/链接类型/实例前后对照计数，从外部可观测地证明
该上界与系统总规模无关，无需了解任何实现细节。

## 本地验证方法

```bash
# 单元与组合边界测试（深度边界、环去重、合并规则、回滚矩阵、错误优先级）
go test ./ontology/

# 随机差分测试（5 个种子 × 8 张随机图 × 40 个随机动作，与参照实现逐项对照）
go test ./ontology/ -run TestDifferentialAgainstReference -v

# 并发串行化（需竞态检测）
go test -race ./ontology/ -run TestConcurrentSerializable -v

# 检查计数上界
go test ./ontology/ -run TestCheckCountIndependentOfSystemSize -v

# 全量 + 竞态 + 静态检查
go test -race ./...
go vet ./...
gofmt -l .

# 演示
go run ./cmd/server
```

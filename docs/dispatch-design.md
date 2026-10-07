# 动作多态分派机制设计说明

面向“一个动作对多种对象类型通用、各类型自注册执行逻辑、运行期可热替换”的
场景，本设计给出确定性的分派规则、并发语义、放宽审计与可验证的复杂度界。

## 1. 核心模型

- `Action`（`types.go`）：通用声明，只有名字、输入形状校验等**接口契约**，
  不含任何执行逻辑。
- `ObjectType`（`object.go`）：有且仅有一条不可变继承来源 `Parent`，
  `ancestorChain` 得到自具体类型向上的链。
- `Logic`（`types.go`）：一套具体执行逻辑 = 自身前置 `Pre` + `Execute`
  + 自身后置 `Post`。它是值对象，注册后不被原地修改。
- `Registry` / `snapshot`（`registry.go`）：每个 `(动作, 类型)` 槽位保存一个
  `installNode`，槽位有三种互斥状态：
  - `stateAbsent`：从未注册（含历史上不存在节点）；
  - `stateActive`：当前直接注册生效；
  - `stateWaived`：该类型**显式放弃**对该动作的直接处理，要求继续使用继承来源。

  每次注册/放弃都用 copy-on-write 生成一个新的不可变 `snapshot`，新版本号
  单调递增；新节点的 `prev` 指针挂住上一版节点。

“显式放弃”和“从未注册”是两种不同的槽位状态，查找结果与错误归类都据此区分，
不会混淆。

## 2. 分派查找规则

`resolveOnChain`（`registry.go`）只沿实例当前绑定具体类型的继承来源链向上：

1. 第 0 层（具体类型）槽位为 `active` → **直接命中**（`HitDirect`）。
2. 某层槽位为 `waived` → 记录“遇到过放弃”并**跳过这一层继续向上**。
3. 某层槽位为 `active` 且深度 > 0 → **间接继承命中**（`HitInherited`）。
4. 走到链顶仍无 `active`：
   - 途中遇到过 `waived` → `no-dispatch:waived-without-inherited`；
   - 一次都没遇到 → `no-dispatch:none-registered`。

直接注册天然优先于间接命中（第 1 步先于向上查找）；显式放弃则强制跳过直接层。
本规则**只查动作逻辑注册表，不查属性取值表**，两套查找各自独立，互不引用结果。

## 3. 调用发起时刻与热替换

`Dispatcher`（`dispatcher.go`）用一把互斥锁串行化所有元数据变更（注册、放弃、
撤销、类型/对象定义）与调用的**分派段**。

- **调用发起时刻（线性化点）= 调用 goroutine 获取该锁、捕获当前不可变快照与
  实例状态的时刻。** 这是“发起”的确定判定标准。
- 分派段在锁内原子完成：取快照 → 读实例绑定类型与撤销位 → 沿链查找 → 记录。
- 之后在**锁外**执行 `Pre/Execute/Post`，使用的是分派段固定下来的
  `*installNode` 与 `*snapshot`。热替换只生成新快照、从不改写旧节点，因此：
  - 替换前已在执行的在途调用：持旧节点指针，**完整用旧逻辑执行到底**；
  - 替换后才发起的调用：线性化点落在替换之后，拿到新快照，**必用新逻辑**。

### 被放弃的方案

- *RWLock + 引用计数延迟回收旧逻辑*：回收时机难推理，引用计数与执行 goroutine
  崩溃交织时易泄漏/悬挂。不可变快照 + 指针自然存活，旧逻辑随最后一个在途调用
  结束后由 GC 回收，无需计数。
- *给每次调用传“版本号”由调用方选择*：把一致性责任推给调用方，无法保证全序。
- *执行期间持锁*：会让一个慢逻辑阻塞全平台分派。本设计只在毫秒级分派段持锁，
  逻辑执行完全并行。

## 4. 四类结果与判定优先级

`DispatchStatus` 明确暴露四类（外加执行器自身的执行错误）：

| 状态 | 含义 | 何时判定 |
| --- | --- | --- |
| `no-dispatch:none-registered` | 具体类型及全部来源均未注册 | 执行任何逻辑**之前** |
| `no-dispatch:waived-without-inherited` | 显式放弃但来源也未注册 | 执行任何逻辑**之前** |
| `object-revoked-during-lookup` | 查找段实例被并发撤销，整体失败 | 分派段内、逻辑启动前 |
| `precondition-failed` / `postcondition-failed` / `execute-failed` | 逻辑自身前置/后置/执行 | 分派成功、逻辑启动**之后** |

优先级严格为：两种无法分派 → 查找段撤销 → 前置/执行/后置。实现中无法分派分支
在读取撤销位之后仍先返回（见 `Invoke`），保证即使实例已撤销，也不会把“无逻辑
可分派”误报成“撤销失败”。

撤销位 `Object.revoked` 是原子布尔。查找段与撤销操作共用同一把锁和同一单调序号，
因此“查找过程中被撤销”不是时间窗猜测，而是**全序上撤销事件排在本次 invoke
线性化点之前**这一确定事实；此时绝不调用 `Execute`，不会产生写入。撤销发生在
逻辑启动之后时分派机制不干预，由逻辑在 `Post` 中读 `obj.Revoked()` 自行裁决
（`TestRevokeDuringExecutionLeftToPostcheck` 演示）。

## 5. 放宽声明与事后审计

热替换若把“旧逻辑拒绝的某类输入”改为放行，注册方必须同时提供一条
`RelaxScope` 元信息（`audit.go`）：`Description()` 说明放宽范围，`Covers(in)`
判定输入是否落在范围内。

系统**不阻止**任何注册。审计是纯事后机制，`Audit` 取槽位当前节点与 `prev`
（即被替换的上一版），在一组探测输入上分别跑新旧逻辑并归类为“拒绝/放行”：

- 旧拒绝 → 新放行，且未提供 `RelaxScope`：`relaxation-not-declared`；
- 旧拒绝 → 新放行，提供了声明但 `Covers` 为假：`relaxation-out-of-scope`
  （实际行为超出声明的放宽范围）；
- 放行变拒绝等其它契约差异：`other-divergence`。

审计只对比**拒绝/放行契约**，不因两版实现自身的标识（输出里的逻辑 ID 等）不同
而误报。审计只读两个历史节点，与在途调用互不干扰。

## 6. 线性一致（与朴素串行等价）

所有 install/waive/revoke/create/invoke 在同一把锁上各取一个单调递增序号，并追加
到只增的事件日志 `[]Event`。因此这些操作天然存在一个全序，每个 invoke 使用的
快照版本就是其在该全序中所处时刻的注册表，**不可能**出现介于两次替换之间、无法
对应任何确定时刻的逻辑。

`TestConcurrentLinearizability` 用一个**独立的朴素串行参照模型**
（`naiveSerialModel`，按事件序号一个个 apply install/waive 再 resolve）重放真实
事件日志，逐条核对每个真实 invoke 实际使用的 `(逻辑, 命中类型, 命中依据)` 与串行
模型完全一致。`TestConcurrentRaceStress` 在 `-race` 下混合 install/waive/invoke/
revoke 验证无数据竞争。

## 7. 复杂度界（可验证）

`resolveOnChain` 只遍历具体类型自身的继承链，且在命中处停止。设命中祖先与具体
类型的实际代际深度为 h，则查找检查层数 `steps = h+1`（直接命中为 1），
**与系统中注册过该动作的其它类型数量 N 无关**：代码路径中不存在对注册表其它键的
扫描，单次只做 `table[action]` 一次 map 定位 + 每层一次 map 查找。

机器可重复的证据见 `TestLookupCostIndependentOfUnrelatedRegistrations`：在无关注册
类型从 0 增长到 1000（N 增长）前后，断言 `DispatchRecord.Steps` 恒为实际链深
（2 / 1 / 2），不随 N 增长。

## 8. 事后核对记录

每次调用产生一条 `DispatchRecord`：全序序号、动作、对象、具体类型、实际遍历路径
`Path`、步数 `Steps`、命中依据、命中类型、逻辑 ID、所钉住的注册表版本、最终状态
与输出/错误。`Dispatcher.Records()` 返回副本，供审计与回归对照，调用方无法篡改
内部记录。

## 9. 本地验证方法

```bash
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/tmp/gocache        # 仅当默认缓存目录只读时需要

go test -race -v ./...             # 全量：分派路径/热替换/审计/线性一致/复杂度
go test -run TestConcurrent -race  # 只看并发线性一致与竞态
go vet ./...
gofmt -l .                         # 无输出即格式干净
```

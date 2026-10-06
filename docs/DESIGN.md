# 多集群副本联邦分配器 — 设计说明

## 1. 职责划分

实现位于 `federation/` 包，五个文件各负其责、通过明确的数据流协作：

| 文件 | 职责 | 关键导出 |
| --- | --- | --- |
| `errors.go` | 四类可区分错误、优先级、统一构造器 | `ErrorCode`、`AllocError` |
| `cluster.go` | 集群值对象、结构性校验、有效上限、深拷贝 | `Cluster` |
| `registry.go` | 并发安全的动态增删改、一致快照、分配入口 | `Registry` |
| `allocator.go` | 纯函数：最小保障 + 多轮权重分摊（big.Int） | 包内 `allocate` |
| `plan.go` | 目标 → 变更计划、增减量、总迁移量 | `Result`、`Change` |

数据流：`Registry.Allocate(total)` 在 `RLock` 下取出**深拷贝快照**
（存活集群 + 删除墓碑），随后在锁外调用纯函数 `allocate`，再由
`buildPlan` 生成变更计划。分配全程不写登记表，因此被拒绝的请求不可能
改变任何集群状态。

## 2. 分配语义（精确定义）

设可用集群集合 A，有效上限 `cap_i = min(Max_i, Capacity_i)`（无 Max 时即
Capacity_i）。不可用集群与已删除集群目标恒为 0，其 Current 进入迁出量。

1. **最小保障**。若存在 `i ∈ A` 使 `Min_i > cap_i`，报**配置冲突**。
   否则目标先置为 `Min_i`；若 `ΣMin_i > total`，报**最小之和超出总数**。
2. **权重参与者** P = `{i ∈ A : Weight_i > 0}`。权重为 0 的可用集群
   只保留 Min，不参与后续分摊。
3. **逐轮 Hamilton 分摊**，池 `R = total − ΣMin`：
   - 一轮参与者 U = P 中尚未 `target = cap` 者，`W = Σ_{i∈U} Weight_i`。
   - 确定份额 `f_i = floor(R·w_i/W)`，小数分子 `n_i = R·w_i mod W`
     （同分母 W，直接比分子即可，无需浮点）。
   - `f_i ≥ headroom_i = cap_i − target_i` 的集群**取上限并饱和**，只吃下
     `headroom_i`；`f_i − headroom_i` 这部分**不在本轮补发**。
   - 取整余量 `L = R − Σ实际锁定的 floor`（`0 ≤ L < |U|`），按
     `n_i` 降序、`Current_i` 降序、名称升序的顺序，给未饱和集群各补至多
     一个；补的过程中触顶者立即饱和，其让掉的余量顺延给后续未饱和者；
     一轮扫描后仍发不出去的余量留在池中。
   - 池扣减本轮所有参与者的实际增量；饱和者退出，回到第一步开新一轮。
4. 池非空但 U 为空，报**容量不足**，消息携带精确缺口 `R`。

### 一个关键取舍：截断份额必须“下一轮再分”，不在同轮回池

规格原文：“凡本轮超过上限的集群取其上限并标记为饱和，**其超出部分与取整
余量一并进入下一轮**”。因此某集群 floor 超过 headroom 时，超出部分不能在
本轮按同一比例继续塞给别人——那样会少一次“按剩余权重重算比例”的机会。
本轮只发出 `锁定floor + 至多L个补一`，其余全部随池进入下一轮。
`federation/allocator.go` 用每轮起始目标 `start[name]` 精确记账实际增量，
保证 `Σtarget ≡ total` 且轮次语义与规格逐字一致。

## 3. 稳定性：Current 只在小数并列时出现

- 排序键依次为「小数分子 → Current（降序）→ 名称」。`Current` 仅在
  `n_i` 严格相等时才被读取，不参与任何 floor、上限、饱和判断。
- 所有迭代集合都由 map 收集后按名称排序或使用确定顺序，登记输入顺序
  不可能影响结果（`TestOrderIndependent` 以打乱顺序双跑对照）。

## 4. 删除即迁出（墓碑）

`Remove` 把集群从 `live` 移到 `tombstone`，保留最后配置（尤其 Current）。
此后快照仍含该条目，但 `dead=true`：目标恒为 0，变更计划中出现
`From=Current, To=0, Delta=−Current`，迁出量计入 Migration。同名集群
重新 `Register` 时清除墓碑，以新配置重新参与分配。

## 5. 错误优先级

`参数非法(1) > 配置冲突(2) > 最小之和超总数(3) > 容量不足(4)`。

- 结构性非法（重名、负权重/容量/Current、nil 整数、`Min>Max`、负 total）
  在登记或分配入口立即拒绝，永远先于任何分配期检查。
- 分配期严格按「先扫配置冲突 → 再比 Min 之和 → 分摊中才可能容量不足」
  的顺序，故同时存在多个问题时只暴露最高优先级类别。
`TestErrorPriority` 在同一登记表上按顺序触发四种类别并逐一断言。

## 6. 并发与一致性快照

- `Register/Update/Remove` 用写锁互斥；`Snapshot`/`Allocate` 用读锁，
  快照是每个集群的**深拷贝**（big.Int 不可变语义上仍复制指针值）。
  分配计算在锁外完成，长计算不阻塞登记修改。
- 因此每次分配所见集合必为某一时刻的一致快照；并发结果集合等价于某个
  串行交错（可线性化）。`TestConcurrency` 以 `-race` 运行 16 worker ×
  300 轮混合读写，只做与顺序无关的不变量校验（Σ=total、无负目标、
  Migration 与逐 Change 重算一致、计划有序）。

## 7. 数值范围与性能

- 全部副本数运算使用 `math/big.Int`：total、权重、容量可达 10^15 乃至
  10^30 以上也不溢出（`TestHugeValues` 直接验证 10^30，Σtarget 精确等于）。
- 比较小数部分用 `R·w mod W` 的整数分子，不引入浮点。
- **开销只取决于集群数 n**：生产代码不存在任何以 total 为上界的循环，
  每轮至少有一个集群饱和退出，故轮数 ≤ 权重参与者数；每轮做 O(k log k)
  排序与 O(k) 次 big.Int 乘除，总体 O(n² log n) 次 big.Int 运算，
  与 total 数值大小无关。
- 可验证证明（`TestCostIndependentOfTotal`）：分配器暴露 `stats`
  （Rounds、ComparePairs、BigIntOps）。同一组集群在
  total = 10 / 10^6 / 10^30 / 10^60 下，三项计数**逐位相同**，并断言
  `Rounds ≤ 权重集群数`。这是对“不随 total 增长”的结构性 + 计数双重
  证据，而非耗时测量。

## 8. 被放弃 / 未采用的方案

- **逐副本除数法（D'Hondt / 领取后比值最小）**：曾作为朴素模型，发现
  “比值并列时按 Current 打破”与最大余数法（Hamilton）在有上限截断时
  不逐副本等价（除数法是不同的分摊制度）。最终朴素模型改为**逐轮
  floor+余数**的独立整数实现，与生产批量代码路径完全独立而语义相同，
  20000 组随机配置逐项一致。
- **浮点比例**：在 10^15 量级 double 仍有精度风险，且并列判定不可靠，
  全部改为 `big.Int` 的商与余数。
- **在同轮把截断份额继续按旧比例塞出**：少一次重算比例的机会，违背
  “进入下一轮”的原文，废弃。
- **分配时持写锁**：简单但长 total（big.Int 位数）会拖慢并发登记；
  改为读锁快照 + 锁外纯计算。

## 9. 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/go-cache
go test -race -v ./federation/          # 全部用例（含并发竞态、详细日志）
go test -cover ./federation/            # 语句覆盖率（当前 95.6%）
go vet ./... && gofmt -l federation/    # 静态检查与格式
```

每个用例把「输入 → 实际输出 → 判定依据」打印到 stdout，并镜像到
`t.TempDir()` 下的 `<用例名>.log`（用例首行打印该路径）。
随机朴素模型对照默认 20000 组（`crosscheck_test.go` 中的 `cases`）。

## 10. API 速览

```go
reg := federation.NewRegistry()
reg.Register(&federation.Cluster{
    Name: "cn-a", Weight: big.NewInt(3), Min: big.NewInt(1),
    Max: nil /* 不限上限 */, Capacity: big.NewInt(100),
    Available: true, Current: big.NewInt(2),
})
res, err := reg.Allocate(big.NewInt(10))
res.Targets            // map[string]*big.Int，Σ == total
res.Changes            // []*Change，仅含 target≠current，按名称排序
res.Migration          // 所有减少量之和
```

错误判定：`var ae *federation.AllocError; errors.As(err,&ae); ae.Code == federation.ErrInsufficientCapacity`。

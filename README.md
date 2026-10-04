# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 利他锁管理器（`ontology` 包）

`ontology.Manager` 实现利他锁（Altruistic Locking）：长事务可通过 `Donate`
提前捐赠已用完的对象供他人加锁，接触过捐赠对象的事务受尾流（wake）规则约束。

### 状态模型

- 构造参数为对象数 `N`（1 到 64，编号 0 到 N−1），否则以配置非法整体拒绝；
  每个对象至多一个持有者（初值无）。
- `Begin()` 返回从 1 起递增的事务号，新事务为活跃态。
- 每个事务维护：
  - `locked`：曾经加锁过的全部对象（含现持有与已捐赠）；
  - 现持有集合；
  - `donated`：捐赠过的全部对象，只增不减；
  - 尾流集合 `wk`：曾在某对象已属 `donated(T)` 的那一刻加锁该对象的各事务 `T`，
    记于加锁成功时（不追溯既往）。

### 调用与拒绝判定顺序

所有调用先做通用判定，只报第一个失败：事务号不存在 → 事务不是活跃态 →
对象越界（仅 `Lock`、`Donate`）→ 各调用专有原因。被拒绝的调用不改变任何状态。

- `Lock(t,o)` 专有判定依次为：
  1. `o ∈ donated(t)`：以已捐赠拒绝（不可重新加锁自己捐出的对象）；
  2. `o` 的持有者是 `t` 自己：成功且无变化；
  3. `o` 被他人持有：以被占用拒绝（先于尾流判定）；
  4. 尾流判定：令 `S' = locked(t)∪{o}`，`C` 为 `wk(t)` 中仍活跃的事务与满足
     `o∈donated(T)` 的其他活跃事务 `T` 之并；对 `C` 中每个 `T` 若 `S'` 不是
     `donated(T)` 的子集，以尾流违规拒绝并报事务号最小的 `T`；
  5. 否则 `t` 成为持有者，`locked(t)` 加入 `o`，满足 `o∈donated(T)` 的其他
     活跃事务 `T` 并入 `wk(t)`。
- `Donate(t,o)`：`o` 须由 `t` 持有（否则以未持有拒绝）；之后 `o` 无持有者，
  `donated(t)` 加入 `o`；`t` 仍活跃，仍可加锁新对象。不做尾流判定。
- `Finish(t)`：若 `wk(t)` 中仍有活跃事务，以尾流未结束拒绝并报最小的 `T`；
  否则释放全部持有并转已结束。
- `Abort(t)`：令 `A={t}`，反复把 `wk` 中含有 `A` 内某事务的活跃事务并入 `A`
  （级联闭包）；`A` 中全部转已中止并释放持有，返回 `A` 的升序列表。
  不做尾流判定，只作用于活跃事务。

### 并发与确定性

所有方法可并发调用，内部由互斥锁串行化，结果等价于某个串行顺序；相同调用
序列重放得到完全相同的结果。任意时刻各对象持有者互不相同；对任一活跃事务
`U` 与其 `wk(U)` 中仍活跃的每个 `T`，`locked(U) ⊆ donated(T)`；成功
`Finish` 的事务其 `wk` 中无活跃事务。尾流判定对活跃事务逐个做位集运算，
检查的事务数不超过活跃事务数。

### 本地验证

```bash
# 确定性用例（题目示例、边界与级联中止等）
go test ./ontology

# 2000 组随机序列与朴素模拟对照，日志打印输入、输出与判定依据
go test ./ontology -run TestRandomSequencesAgainstNaive -v

# 并发线性一致性与竞态检测
go test -race ./ontology
```

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

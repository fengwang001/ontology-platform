# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多边净额轧差清算器（`netting` 包）

`netting.Engine` 按清算周期汇总参与方之间的应付义务，在净借记超限时
连带撤销违约方义务并重算，最终生成确定性的划付指令。所有方法均带
互斥保护，`Register` / `Submit` / `Close` 可并发调用，结果等价于某个
串行顺序；`Close` 是原子步骤。

### API 与拒绝顺序

- `NewEngine()` 创建清算器，当前周期号为 1。
- `Register(id, cap)`：`id` 非空、`cap` 在 `[0, 10^15]`；否则返回
  `ErrInvalidArgument`，重复登记返回 `ErrDuplicateParty`。
- `Submit(oid, from, to, amt)`：`oid` 非空、`amt` 在 `[1, 10^12]`。
  错误只报第一个，顺序为：参数非法 → 义务编号重复（同一周期内）→
  参与方未登记（先查 `from` 再查 `to`）→ 自身对手 → 周期已满
  （每周期至多 `10^5` 条）。
- `Close()` 清算当前周期并开启新周期（旧义务清空、参与方保留、
  周期号从 1 起递增、新周期 `oid` 可重用）。被拒绝的操作不改变
  任何参与方、义务与周期。

### 净头寸与违约级联

1. 取全部有效义务，算每方净头寸 `net = 应收之和 − 应付之和`。
2. 若某方 `net < 0` 且 `-net > cap`（**恰等不违约**），该方违约；
   同一轮的全部违约方在同一快照上**同时认定**（不是逐个认定后
   立即重算），按 `id` 字节序升序记录。
3. 把 `from` 或 `to` 为违约方的全部义务一并撤销，违约方此后不再
   有任何义务；回到第 1 步重算，直到某一轮没有违约方。
4. `CloseResult.Defaulters` 按认定轮次、轮内 `id` 升序；
   `RevokedOIDs` 按字节序升序；`Positions` 是全体已登记参与方
   （含违约方与零头寸方）的最终净头寸，按 `id` 升序。

### 划付指令的排序与撮合

用最终有效义务得到净头寸后：

- 付方（`net < 0`）与收方（`net > 0`）分别按金额绝对值降序、
  `id` 升序排队；
- 反复取两队队首，划付 `min(付方剩余, 收方剩余)`；
- 剩余为 0 的出队，剩余不为 0 的**留在队首且不重新排序**，直到
  全部为 0；
- 指令按产生次序输出（付方、收方、金额）。

不变量：最终净头寸之和为 0；非违约方满足 `-net <= cap`；
付出总额等于收到总额；指令条数不超过非零净头寸参与方数减 1。

### 本地验证

```bash
# 全部测试（含 2000 组随机朴素模拟对照、并发可串行化测试）
go test -race -v ./netting/

# 只看 2000 组随机对照的输入/输出/判定依据日志
go test -run TestRandomDifferential -v ./netting/

# 跳过 10 万条义务上限的用例
go test -short ./netting/

go test -race ./...
gofmt -l .
go vet ./...
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

# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多边净额轧差清算器（`netting` 包）

`netting.Engine` 按清算周期汇总参与方间应付义务，处理净借记超限的
违约级联，并输出确定性的最终划付指令。所有方法均在互斥锁内完成，
`Close` 是原子步骤；同一周期义务以任意顺序提交，`Close` 结果逐字段相同。

### API

- `NewEngine()`：创建引擎，首个周期号为 1。
- `Register(id, cap)`：登记参与方。`id` 为非空字节串，`cap` 为净借记上限，
  范围 `[0, 10^15]`；重复登记返回 `ErrDuplicateParty`。
- `Submit(oid, from, to, amt)`：向当前周期登记义务「from 应付 to 金额 amt」。
  `oid` 非空、`amt` 范围 `[1, 10^12]`，每周期至多 `10^5` 条。
- `Close() CycleResult`：清算当前周期并开启新周期（义务清空、参与方保留）。
- `CycleNo()`：返回下一次 `Close` 将结算的周期号。

### 净头寸与违约级联

1. 取当前周期全部有效义务，计算 `net = 应收之和 − 应付之和`。
2. 若某方 `net < 0` 且 `-net` **严格大于** `cap`（恰等不算）则为违约方。
   同一轮的全部违约方**同时认定**（按 id 字节序升序记录为一轮），
   凡 `from` 或 `to` 触及任一违约方的义务一并撤销，然后回到第 1 步重算。
   违约方被认定后不再保留任何义务，且不会在后续轮次重复出现。
3. 重复直到某一轮没有违约方；`Revoked` 为全部被撤销义务的 oid，按字节序升序。

### 划付指令的排序与撮合

用最终有效义务计算净头寸（全体已登记参与方均输出，含 0 头寸，按 id 升序）：

- 付方队（`net < 0`）与收方队（`net > 0`）各按金额绝对值降序、id 升序排列。
- 反复取两队队首划付 `min(付方剩余, 收方剩余)`；剩余为 0 的出队，
  剩余不为 0 的留在队首且**不重新排序**。
- 指令按产生次序返回（付方、收方、金额）。
- 指令条数不超过非零净头寸参与方数减 1；最终净头寸之和为 0，
  付出总额等于收到总额，非违约方均满足 `-net <= cap`。

### 参数校验（`Submit` 按此顺序只报第一个）

1. 参数非法（空 `oid`、`amt` 越界）—— `ErrInvalidArgument`
2. 义务编号在本周期重复 —— `ErrDuplicateObligation`
3. 参与方未登记（先查 `from` 再查 `to`）—— `ErrUnknownParty`
4. 自身对手（`from == to`）—— `ErrSelfCounterparty`
5. 周期已满 —— `ErrCycleFull`

被拒绝的操作不改变任何参与方、义务与周期状态；新周期可重用旧 oid。

### 本地验证

`go` 不在默认 PATH 时先执行 `export PATH=$PATH:/usr/local/go/bin`；
若默认构建缓存只读，追加 `GOCACHE=/tmp/gocache`。

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细输出（含 2000 组随机对照的输入/输出/判定依据日志）
go test -race -v ./netting

# 仅看 2000 组随机差分对照日志
go test -v -run TestRandomDifferential ./netting

# 代码检查
gofmt -l .
go vet ./...
```

`TestRandomDifferential` 用固定种子生成 2000 组参与方/上限/义务集合，
对每组：以两种不同提交顺序驱动引擎验证结果一致、与逐轮规则写成的
朴素模拟器 `naiveSettle`（`netting/naive.go`，并含逐个认定变体用于反证
同时认定语义）逐字段对照，并校验全部代数不变量；日志打印每组的
输入、输出与判定依据。

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

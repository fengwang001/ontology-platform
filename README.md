# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多版本键值垃圾回收器

`ontology.NewKVGC(R)` 创建记录总数上限为 `R`（`1..10^6`）的多版本键值垃圾回收器。所有公开方法内部由互斥保护，并发调用等价于某个确定的串行顺序。

### 记录与读取

- `Write(key, type, ts, value)` 写入四类记录：`P`（Put，value 可为空串）、`D`（Delete）、`L`（Lock）、`R`（Rollback）。
- `D`、`L`、`R` 的 value 必须为空；键必须非空；记录时间戳范围为 `1..10^15`。
- 同一键、同一 `ts` 只能有一条记录。
- `Get(key, ts)` 从该键所有 `ts' <= ts` 的记录中，选择时间戳最大的 `P` 或 `D`；`L` 与 `R` 不影响读结果。
- 命中 `P` 返回其值；命中 `D` 或没有可见的 `P/D` 返回不存在。

错误按优先级区分为：`ErrInvalidArgument`、`ErrExpired`、`ErrDuplicate`、`ErrFull`、`ErrRolledBack`、`ErrSnapshotBlocked`、`ErrSnapshotNotFound`。

### 安全点与快照

- 安全点初值为 0，`SetSafePoint(sp)` 只接受 `0..10^15` 且不得小于当前安全点。
- 当存在打开快照时，新安全点不得大于所有打开快照时间戳的最小值；等于最小快照时间戳允许。
- `Write` 的 `ts <= safePoint`、`Get` 与 `OpenSnapshot` 的 `ts < safePoint` 都会返回过期错误。
- 被拒绝的操作不会修改记录、安全点、游标或快照。

### 分轮分批回收

成功调用 `SetSafePoint` 会开启新的一轮，并把游标清空；相等安全点重复设置也会重置当前轮。

`GCStep(n)` 按键的字节序，选择严格大于游标的最小 `n` 个仍存在的键，处理后将游标移动到本批最大键；返回实际处理数量。没有可处理键时返回 0。本轮中新增的键若小于等于游标，留到下一轮；若大于游标，可在本轮后续步骤处理。

对某个键在安全点 `sp` 的处理规则为：

1. 访问计数只增加该键 `ts <= sp` 的记录条数，不访问 `ts > sp` 的记录。
2. 删除所有 `ts <= sp` 的 `L` 与 `R`。
3. 在 `ts <= sp` 的记录中找到时间戳最大的 `P` 或 `D`，记为 `X`。
4. `X` 为 `P` 时保留 `X`，删除所有 `ts < X.ts` 的记录；`X` 为 `D` 时连 `X` 一并删除。
5. 没有 `X` 时，除前述过期 `L/R` 外不删除其他记录；所有 `ts > sp` 的记录保持不变。
6. 一个键的记录全部删除后，该键从有序索引中消失。

### 读结果不变性

对任意合法查询 `ts >= sp`，设回收前可见的最新 `P/D` 为 `X`：

- 若 `X.ts > sp`，GC 不访问也不删除任何 `ts > sp` 的记录，因此查询仍由 `X` 或时间戳更大的未动记录决定。
- 若 `X.ts <= sp` 且 `X` 为 `P`，GC 保留 `X`，只删除更早版本；这些更早版本原本就被 `X` 遮蔽。
- 若 `X.ts <= sp` 且 `X` 为 `D`，GC 删除 `X` 和更早版本；对所有查询时间 `ts >= sp >= X.ts`，被删除的删除标记表达的仍是“不存在”，查询结果不变。
- `L/R` 不参与 `Get`，删除它们不会改变任何读结果。

因此，在任意次 `GCStep` 前后，对所有 `ts >= safePoint` 的 `Get` 结果完全相同。相同操作序列还会产生相同快照号、记录、游标与逐键访问计数。

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
go test -run TestRandomNaiveComparison -v ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

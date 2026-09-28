# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 行级过滤视图增量维护（`filterview`）

`filterview` 包对源表维护一个取值区间过滤视图，随插入、删除、更新实时输出净变化（change log）。下游只要按日志顺序应用 `ADD` / `RETRACT`，就能始终重建出正确的过滤视图。

### 过滤条件

- 视图 = 源表中 `Value` 满足左闭右开区间 `[Low, High)` 的全部行。
- 端点规则：`Value == Low` 属于视图；`Value == High` 不属于视图。
- `Low >= High`（空区间或倒置区间）为非法参数，`New` 返回 `ErrInvalidParams`。

### 输入与输出规则

- **插入**：新值满足条件 → 输出一条 `ADD`；不满足 → 无输出。
- **删除**：被删行当前值满足条件 → 输出一条 `RETRACT`；不满足 → 无输出。
- **更新**：先按旧值/新值是否满足条件分四种情形：
  1. 旧在内、新在内且取值不变 → **无输出**。
  2. 旧在内、新在内但取值变化 → 先 `RETRACT` 旧行，再 `ADD` 新行（严格有序）。
  3. 旧在内、新在外 → `RETRACT` 旧行。
  4. 旧在外、新在内 → `ADD` 新行。
  5. 两侧都在外 → 无输出。

每条净变化带全局递增序号 `Seq` 与判定依据字符串 `Basis`。空批次为合法无操作。

### 拒绝原因（可区分的哨兵错误）

用 `errors.Is(err, filterview.ErrXxx)` 区分：

- `ErrInvalidParams`：区间非法，或出现未知操作类型。
- `ErrEmptyKey`：插入/删除/更新的行标识为空。
- `ErrKeyMismatch`：更新前像与新像的主键不同。
- `ErrKeyExists`：插入的主键在源表已存在。
- `ErrKeyNotFound`：删除/更新的主键在源表不存在。
- `ErrBeforeImageDiff`：更新前像与源表当前行（主键和取值）不一致。

`Apply` 以批为单位原子提交：先在源表影子拷贝上逐条校验与计算，任一条目被拒绝则整批放弃，源表、视图与已产生日志均不改变；错误信息标出条目下标与具体原因。

### 一致性与确定性

- 内部以 `sync.RWMutex` 保护：写操作串行化，读操作（`Snapshot`、`Log`、`Get`、`Contains`、`SourceSnapshot`）可并发，且均返回深拷贝，读侧看到的源表/视图/日志逐字段一致。
- 输出仅由输入序列决定（快照按 Key 排序，日志按序号排列），同一输入序列反复运行得到完全相同的输出。

### 本地验证

```bash
# 单元测试（更新四情形、区间端点、全部非法输入、批原子性、并发快照一致性、确定性、日志顺序回放）
go test -race -v ./filterview

# 端到端演示：打印每条输入、判定依据与净变化
go run ./cmd/filterview-demo
```

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

## 单键表与可延迟唯一约束（`ontology` 包）

`ontology` 包实现行号（非空字符串）到键的映射：键为 `NULL` 或字符串。

- `NULL` 与 `NULL` 互不相等，多行可同为 `NULL`；空串 `""` 与 `NULL` 不同，空串本身仍受唯一约束。
- 其余键按字节相等判同；行号按字节序排序。
- 构造：`NewTable(deferrable, initiallyDeferred bool)`；`initiallyDeferred` 为真而 `deferrable` 为假返回 `ReasonInitiallyDeferredRequiresDeferrable`。
- 并发：所有方法在同一把互斥锁下串行，`Get`/`Keys` 读事务内视图、无事务时读已提交状态；两个并发 `Begin` 恰有一个成功。

### 检查时机表

| 约束 / 模式 | 检查时机 | 检查范围 |
| --- | --- | --- |
| 非可延迟（恒为 IMMEDIATE） | 每个 `Insert`、`Update` 之后立即检查 | 仅该行的新键 |
| 可延迟 + IMMEDIATE | 整条语句全部 op 执行完后检查一次 | 本语句内被 Insert/Update 过的行的当前键 |
| 可延迟 + DEFERRED | 语句内不检查 | 终检时检查事务内被 Insert/Update 过且仍存在的行 |

违例定义：某行键非空，且另有行持有相同的键。被检查集合中有多种违例键时，
错误（`*ontology.Error` 的 `ViolatedKey`）取键的字节序最小者；
语句内即时检查失败时 `OpIndex` 为该 op 下标，语句末/`SetMode`/`Commit` 检查为 `-1`。

### 语句、模式与事务

- `Apply(ops)` 是一条语句：顺序执行；非可延迟约束在某个 op 后立即违例、
  或可延迟即时模式语句末检查违例时，整条语句的全部 op 撤销，事务仍有效。
- op 报错次序（按 op 顺序取第一个）：行号为空 →
  Insert 行号已存在 / Update、Delete 行号不存在 → 即时检查违例。
- `SetMode(mode)` 仅可延迟约束可用，报错顺序：无事务 → 模式非法 → 约束不可延迟。
  - 切到 `IMMEDIATE`：立即对事务内至今触碰过（且仍存在）的行检查一次；违例则拒绝，模式与状态不变。
  - 切到 `DEFERRED`：只改模式。
- `Commit`：延迟模式先做一次终检，违例则整个事务回滚（已提交状态不变）并返回违例错误；
  即时模式直接提交。`Rollback` 放弃事务。`Commit`/`Rollback` 后无活动事务。
- 被删除的触碰行不再参与后续语句末检查、`SetMode` 检查与提交终检。

### 错误原因

`*ontology.Error` 的 `Reason` 区分：已有事务、无事务、模式非法、约束不可延迟、
构造参数冲突、空行号、行号已存在、行号不存在、唯一违例。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素模拟器对拍；-v 可查看每组输入/输出/判定依据日志）
go test -v ./ontology

# 竞态检测（含两个并发 Begin 恰一个成功等用例）
go test -race -count=1 ./...

# 仅运行对拍
go test -run TestDifferentialAgainstNaive -v ./ontology

go vet ./...
gofmt -l .
```

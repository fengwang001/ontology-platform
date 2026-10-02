# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## uniquetable：带可延迟唯一约束的单键表

`uniquetable` 包实现行号（非空字符串）到键的映射，键为 NULL 或字符串：
NULL 互不相等（多行可同为 NULL），空串与 NULL 不同，其余按字节相等判同。
构造参数 `(deferrable, initiallyDeferred)`，`initiallyDeferred` 为真要求
`deferrable` 为真（否则返回 `ErrInvalidInitialMode`）。同一时刻至多一个事务。

### 检查时机表

| 约束类型 / 事务模式 | Insert/Update 每个 op 后 | 语句（Apply）结束时 | SetMode(IMMEDIATE) | Commit |
| --- | --- | --- | --- | --- |
| 非可延迟（恒为即时） | 立即检查该行新键 | 不检查 | 不可用 | 直接提交 |
| 可延迟 + IMMEDIATE | 不检查 | 一次检查本语句 Insert/Update 过的行的当前键 | 检查事务内至今 Insert/Update 过的行（已删除的除外） | 直接提交 |
| 可延迟 + DEFERRED | 不检查 | 不检查 | 同上，违例则拒绝切换 | 提交前检查一次，违例则整个事务回滚 |

违例指某行键非空且另有行持有相同键；多个违例时错误（`*ViolationError`）
携带字节序最小的违例键。

### 模式切换规则

- `Begin` 时模式重置为初始模式：可延迟且 `initiallyDeferred` 为 DEFERRED，
  否则为 IMMEDIATE；非可延迟约束恒为 IMMEDIATE。
- `SetMode` 仅可延迟约束可用；无事务时报 `ErrNoTx`，模式非法报
  `ErrInvalidMode`，约束不可延迟报 `ErrNotDeferrable`，按此顺序只报第一个。
- 切到 IMMEDIATE 立即检查事务内至今被 Insert/Update 过的行（已被删除的
  除外）的当前键，违例则拒绝且模式与状态不变；切到 DEFERRED 只改模式。

### 回滚范围

- 语句（`Apply`）失败：本语句的全部 op 撤销，事务仍然有效。
- `SetMode(IMMEDIATE)` 被拒：模式与状态均不变。
- `Commit` 在延迟模式下检查违例：整个事务回滚，已提交状态不变。
- `Rollback`：放弃当前事务的全部修改。

### 本地验证

```bash
# 定向用例（检查时机、回滚范围、违例键、NULL 语义、错误顺序、并发 Begin）
go test -v ./uniquetable/

# 与逐步朴素模拟对拍 2000 组随机操作序列（日志含输入、输出与判定依据）
go test -run TestDifferentialRandomSequences -v ./uniquetable/

# 竞态检测
go test -race ./uniquetable/
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

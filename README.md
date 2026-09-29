# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## firstrow：变更流保留首条去重

`firstrow` 包在变更流上按每个键保留排序键最小的首条行，并输出首条变化日志，
下游按顺序应用日志即可始终得到每个键的正确首条。

### 排序与首条判定

- 每行的排序键为 `(Time, ID)`：先按 `Time` 升序（可为负值），`Time` 相同再按 `ID` 字典序升序。
- 每个键的存活行中排序键最小的一行即首条。

### 输出规则

- 逐条处理变更，处理后若首条变化，先输出一条 `RETRACT`（旧首条）、再输出一条 `INSERT`（新首条）。
- 首条不变（如写入/撤回非首条行）则不产生任何输出。
- 存活行清空时只输出 `RETRACT`；从空键写入时只输出 `INSERT`。

### 校验与拒绝

整批变更先在克隆状态上试算，`Apply` 中任一条非法即整批拒绝，存活行与已产生的日志均不变。
拒绝原因可用 `errors.Is` 区分：

| 原因 | 条件 |
| --- | --- |
| `ErrEmptyKey` | 键为空 |
| `ErrEmptyID` | 标识为空 |
| `ErrDuplicateID` | 插入已存活的标识 |
| `ErrMissingID` | 撤回不存在的标识 |
| `ErrTooManyRows` | 存活行数超过 `New(maxLive)` 的上限 |

### 并发与确定性

所有方法均可并发调用（读写锁保护），结果逐键一致；
同一输入序列反复计算得到完全相同的输出日志。

### 本地验证

```bash
go test -race -v ./firstrow/   # 日志打印输入、输出条目与判定依据
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

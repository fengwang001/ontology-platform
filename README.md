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

## lag 包：前驱值（LAG）增量维护

`lag` 包对按分区排序的行变更流增量维护每行的前驱值，并输出确定顺序的
变更日志，使下游按序应用后视图与批量重算一致、与插入顺序无关可复现。

### 排序与前驱取值

- 行按 `Partition` 分组，不同分区互不影响。
- 分区内按 `SortKey` 升序排列，并列按 `ID` 升序打破。
- 某行的前驱值 = 同分区内紧邻其前那一行的 `Value`。
- 分区首行前驱为空（`Prev == nil`），与前驱取值为 `0` 严格区分。

### 变更日志规则

- `Apply` 按输入变更顺序逐条处理，同一输入的输出顺序固定。
- 插入：先输出新行的前驱（`LogUpsert`），再修正紧邻其后的行。
- 删除：先输出被删行（`LogDelete`），再修正紧邻其后的行。
- 前驱值未变的行不输出任何条目。
- 每条日志带单调递增的 `Seq`，下游按 `Seq` 升序应用即可。

### 边界与错误类别

非法输入整批拒绝，不产生日志、不改变行与视图（失败不留痕）。
错误均为可 `errors.Is` 区分的哨兵错误：

| 错误 | 场景 |
| --- | --- |
| `ErrEmptyID` | 行标识为空 |
| `ErrDuplicateID` | 插入已存在的标识（含同批内重复） |
| `ErrRowNotFound` | 删除不存在的标识 |
| `ErrEmptyPartition` | 分区名为空 |
| `ErrTooManyRows` | 提交后总行数超过 `New(maxRows)` 上限 |

### 并发

`View` 与 `SelfCheck` 可被多个执行体并发调用，且可与 `Apply` 提交并发；
`SelfCheck` 校验增量视图与批量重算（`BatchView`）一致。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -count=1 ./...

# 查看每步输入、输出与判定依据
go test -v ./lag/
```

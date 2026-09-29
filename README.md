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

## 增量左外连接组件（`join` 包）

`join.Joiner` 随左右两表的增删实时维护左外连接结果，输出确定性的结果日志，
下游按顺序应用日志即可始终得到正确的左外连接视图。

### 核心规则

- **空填充行**：左表每一行要么与同键的所有右行配对（每个右行一条配对结果），
  要么在没有任何同键右行时以一条空填充行（`RightID` 为空）存在，二者互斥。
- **撤回补回**：首条同键右行到达时，先撤回空填充行再输出配对行；
  删除最后一条同键右行时，先撤回配对行再补回空填充行。同一左行的这两条
  输出在日志中相邻。
- **输出顺序**：每条变更产生的输出条目按对侧标识的字典序排列
  （左行变更按右标识、右行变更按左标识），全局序号 `Seq` 严格递增。
- **原子批**：`Apply` 先整批校验再应用。空键（`empty-key`）、空标识（`empty-id`）、
  插入已存在标识（`duplicate-id`）、删除不存在标识（`missing-id`）、
  总行数超限（`too-many-rows`）都会以 `*RejectError` 拒绝，
  被拒绝的批不改变两表与已产生的日志。
- **并发与确定性**：`View`/`Log`/`Size` 可并发读取且逐行一致；
  同一输入序列反复计算得到完全相同的输出。

### 本地验证

```bash
go test -race -v ./join   # 测试日志会打印输入、输出条目与判定依据
go test ./...             # 全量测试
```

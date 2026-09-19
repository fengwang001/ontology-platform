# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分组聚合器（package ontology）

按有序分组键对 `map[string]any` 行分组，计算 Count 与 Sum。

- **求和顺序无关**：所有有限数值精确累加进 `big.Rat`（有理数加法精确、
  可交换、可结合），仅在读取时做一次 float64 舍入，因此任意输入顺序下
  `math.Float64bits` 逐位一致。NaN 跳过并计数；±Inf 参与求和（IEEE 754）。
- **缺失语义三分**：属性不存在（`PartAbsent`）、值为 nil（`PartNil`）、
  空字符串（`PartEmpty`）分别落入三个可辨认的组，行不会被丢弃。
- **int64 溢出**：全 int64 组的精确和超出 int64 时，`Snapshot` 返回
  `*OverflowError`（可用 `errors.As` 判定），并指出所属组。
- **输出排序**：按分组键逐列升序；缺失列按 `absent < nil < empty` 排在
  所有实际值之前，实际值按"类型标签|值"的规范字符串字典序排列。
  输出与 map 迭代顺序无关，重复聚合逐元素一致。
- **并发**：`Add` 与 `Snapshot` 互斥，单次 `Add` 原子完成，
  `Snapshot` 不会看到半更新状态。

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

# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## dedup：多源并集增量去重维护器

`dedup` 包按分区引用计数维护所有分区的去重并集视图，元素仅在没有任何分区再持有它时才从视图撤下，且与批量重算结果一致。

### 核心规则

- **分区集合**：每个分区是一个集合，同一元素在同一分区内至多出现一次。
- **引用计数**：全局引用计数 = 当前持有该元素的分区个数。
- **幂等语义**：加入已在该分区的元素、撤回不在该分区的元素均为无操作，不改变状态也不产生日志。
- **去重并集视图**：视图为所有引用计数 >= 1 的元素集合；加入时计数 0->1 输出一条 `+` 条目，撤回时计数 1->0 输出一条 `-` 条目，其余情况无输出。
- **变更日志**：按操作顺序追加（`Seq` 单调递增），下游按序应用日志即可重建视图。
- **批量原子性**：`AddBatch` / `RemoveBatch` 先整体校验再应用，任一条被拒则整批不生效，分区集合、引用计数、日志与视图均不变。
- **错误**：分区越界（`ErrPartitionOutOfRange`）、元素为空（`ErrEmptyElement`）、分区数非正（`ErrInvalidPartitionCount`）为三个互不相同的哨兵错误，可用 `errors.Is` 判定。
- **并发**：加入/撤回与视图/日志/自检读取均可并发调用；并发只读同一实例得到的视图逐字段相同。

### 示例

```go
m, _ := dedup.NewMaintainer(2)
m.Add(0, "x") // 计数 0->1，日志追加 {0 + 0 x}
m.Add(1, "x") // 计数 1->2，无输出
m.Remove(0, "x") // 计数 2->1，无输出，x 仍在视图
m.Remove(1, "x") // 计数 1->0，日志追加 {1 - 1 x}，x 撤下
```

### 本地验证

```bash
go test -race -v ./dedup/   # 竞态检测 + 打印输入/结果/判定依据
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

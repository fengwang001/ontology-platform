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

## 双时态快照导出器

实现位于 `bitemporal/`，设计见 `docs/bitemporal-design.md`。

```go
store := bitemporal.NewStore(0 /*保留最早事务时间*/, 0 /*初始时钟*/)
store.RegisterSchema("person", 0, map[string]FieldKind{"name": KindString})

exp := bitemporal.NewExporter(store, bitemporal.NewTextLogger(os.Stdout))
cutoff, err := exp.Prepare(T, "person", nil)        // 一次性冻结 (T, Seq)
snap, err := exp.ExportObject(cutoff, "person", id, nil) // 可跨对象/分批复用
// snap.Segments 中 Value == nil 的段即“未知”，不以默认值填充。
```

关键性质：左闭右开有效时间；`TxTime <= T` 中取最新（同时绑定到达序号，统一
`TxTime == T` 的归属口径）；更正记录在导出中折叠、在 `AuditView` 中保留；点查
比较次数与对象总历史规模无关（规范覆盖段树，64 条链 + 二分）；并发结果等价于
某个全局串行顺序。

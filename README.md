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

## 双时态溯源子系统

`bitemporal` 包实现跨链接的双时态（有效时间 + 写入时间）AsOf 溯源查询，
支持区间追溯修正、源对象/链接/目标对象三方共同可见性约束、多跳深度
遍历、两类不可见原因区分、并发可串行化与候选规模局部有界。

```bash
# 运行演示：边界修正、两类不可见、多跳截断
go run ./cmd/provenance-demo

# 全量测试（含竞态检测）
go test -race ./...
```

最小用法：

```go
store := bitemporal.NewStore()
store.AppendObject(bitemporal.ObjectRecord{ID: "A", /* ... */})
store.AppendLink(bitemporal.LinkRecord{ID: "L", SourceID: "A", TargetID: "B", /* ... */})

engine := bitemporal.NewEngine(store, bitemporal.NewMemoryLogger())
res, err := engine.AsOf(bitemporal.Query{
    SourceID: "A",
    ValidAt:  t,   // 业务有效时间点
    AsOf:     tw,  // 写入时间点（只看到不晚于该时刻落定的记录）
    MaxDepth: 3,
})
```

设计取舍、被放弃方案、并发与有界性论证、测试矩阵见
[`docs/design.md`](docs/design.md)。

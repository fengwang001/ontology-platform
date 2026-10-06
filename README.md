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

## 端点分片维护器（endpointshard）

`ontology/endpointshard` 提供服务端点在固定容量分片间的稳定落位、
增量同步的最小变更、至多一次的合并整理、精确的变更报告与代次、
消费者按区域查询（就绪优先，可服务且终止中回退，同区域优先）以及
分片容量调整。设计取舍与验证方法见 `docs/endpointshard-design.md`。

```go
m := endpointshard.NewManager()
_ = m.CreateService("svc", 2) // 每分片最多 2 个端点
rep, _ := m.Sync("svc", []endpointshard.Endpoint{
	{ID: "a", Region: "cn", Healthy: true},
	{ID: "b", Region: "us", Healthy: true},
})
res, _ := m.Query("svc", "cn") // 同区域在前；无就绪端点时回退
```

```bash
# 该包的测试（含朴素模型随机对照、并发与性能可验证性测试）
go test ./endpointshard -race -v
go test ./endpointshard -bench . -benchmem
```

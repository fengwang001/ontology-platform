# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 交通事件影响传播与排队回溢推演（`traffic` 包）

事件驱动地推演事件造成的通行能力削减、排队形成与向上游的回溢、多事件合成、
解除后消散，以及任意时刻受影响范围查询。核心特性：

- 分段线性解析推演，结构变化只在精确时刻发生；一次推进与分多次推进逐字段一致。
- 回溢约束用最小不动点松弛求解（环路收敛、不重复限制）；等级用多源 BFS，多路径取最小。
- 时刻单调，错误按固定类别次序只报最先一类；所有方法加锁，并发可串行化、重放确定。
- `Query` 为 O(1)，开销不随未受影响路段数量增长。

关键取舍、被放弃方案与验证方式见 `traffic/DESIGN.md`；模型语义见 `traffic/doc.go`。

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

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

## 发言权控制服务（floorcontrol）

`floorcontrol/` 是一个带限时发言权、举手排队、强制静音与主持人移交的
会议室控制模块，支持并发调用且结果可重放。设计与取舍见
`floorcontrol/DESIGN.md`。

```bash
# 随机差分（1500 组，对照独立朴素模型）+ 竞态 + 复杂度基准
go test ./floorcontrol/... -count=1 -v
go test -race ./floorcontrol/...
go test -bench BenchmarkQueueOps ./floorcontrol/
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

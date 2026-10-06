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

## 商家配送范围围栏与动态收缩（fencing）

`fencing/` 实现商家配送范围围栏与动态收缩系统：环距分层基础范围、平台/商家
收缩等级叠加、订单生命周期与在途免疫、只读预检查询、线性一致并发。
设计取舍见 `DESIGN.md`；验证：`go test -race ./fencing`，
性能证明：`go test -bench=. -run=^$ ./fencing`。

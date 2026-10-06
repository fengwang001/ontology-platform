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

## 引用更新事务处理器（refstore）

`refstore` 包实现代码托管服务端的引用更新事务处理器：对一批
「引用名、期望旧值、新值、是否强制」的更新指令做全有或全无的裁决，
逐条给出可区分的拒绝原因，成功后追加序号严格递增的审计记录。

- 设计说明：[DESIGN.md](DESIGN.md)
- 运行测试：`go test ./refstore/ -v -race`

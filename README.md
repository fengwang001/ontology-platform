# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `aries`：ARIES 风格的事务回滚与崩溃重启撤销阶段模型（保存点部分回滚、
  跨事务按 LSN 从大到小的重启撤销、可中断续做的 `RestartStep`）。
  详见 [aries/README.md](aries/README.md)。

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

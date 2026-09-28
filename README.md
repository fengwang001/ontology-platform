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

## 组件

- `lwwset/`：LWW 元素集合 CRDT（最后写入者胜出、并列时间戳偏删除、
  支持两两整份合并与按变更序号的增量合并、并发安全）。
  规则、边界与错误类别见 `lwwset/README.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```

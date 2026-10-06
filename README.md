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

## 请求分帧判定器

增量 HTTP/1.x 请求头、定长消息体和分块消息体判定位于 `framer` 包。设计说明、走私歧义取舍、复杂度证明和验证命令见 `docs/framer.md`。

```go
decoder := framer.NewDecoder(framer.Limits{
    MaxHeaderBytes: 1 << 20,
    MaxBodyBytes:   16 << 20,
})
events := decoder.Push(incomingBytes)
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

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

## 本地验证

```bash
# 普通全量测试；-v 会打印输入、输出与判定依据
go test -v ./...

# 并发竞态检测
go test -race ./...

# 若当前 shell 找不到 go，可显式使用本机安装路径
PATH=/usr/local/go/bin:$PATH go test -v ./...

# 若默认 Go 缓存目录只读，可指定临时缓存
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache GOPATH=/tmp/go-path go test -race ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

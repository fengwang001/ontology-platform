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

## cors 预检内核

`cors` 包实现跨源请求的预检判定与预检结果缓存内核，由请求分类、
预检必要性判定、预检结果缓存、响应校验与凭据模式五部分协作组成。
设计取舍见 [cors/DESIGN.md](cors/DESIGN.md)。

```bash
# 单元测试 + 朴素模型随机对照
go test ./cors/

# 并发等价性（竞态检测）
go test -race ./cors/

# 性能证明：判定耗时与配置规模、缓存规模无关
go test ./cors/ -run=NONE -bench=.
```

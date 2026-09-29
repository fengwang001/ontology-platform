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

## 版本向量墓碑回收器

`ontology` 包实现基于版本向量的因果存储与稳定向量墓碑回收，规则说明见
[ontology/DESIGN.md](ontology/DESIGN.md)：向量按分量顺序字典序判新旧、
稳定向量取各副本时钟逐分量最小值、仅回收向量逐分量不超过稳定向量的
墓碑，旧事件不会复活已删键。

## 代码检查

```bash
gofmt -l .
go vet ./...
```

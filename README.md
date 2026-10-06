# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前仓库包含带唯一二级索引的主表存储与崩溃后索引追赶恢复服务。核心设计见 [DESIGN.md](DESIGN.md)，API 示例见 [USAGE.md](USAGE.md)。

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

# 若默认 GOCACHE 在只读文件系统中：
GOCACHE=/tmp/ontology-go-cache go test ./...

# 带竞态检测与详细输出
go test -race -v ./...
GOCACHE=/tmp/ontology-go-cache go test -race -v ./...

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
GOCACHE=/tmp/ontology-go-cache go vet ./...
```

## 存储服务覆盖点

- 主表、连续写入日志和唯一二级索引职责分离。
- 唯一冲突基于主表最新状态，O(1) 判定，不依赖落后索引。
- 崩溃后索引可领先水位任意前缀长度，并从水位后幂等追赶。
- 支持分批推进、日志缺口检测、追赶期间读写、自检三类不一致分类。
- 随机写入/崩溃序列与独立朴素模型对照，固定种子可复现。

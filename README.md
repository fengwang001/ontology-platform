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

## 多集群副本联邦分配器

`fedalloc` 包实现最小保障、权重分摊（最大余数法）、上限饱和后再分配、不可用集群
迁出与全有或全无拒绝；支持动态登记、精确错误优先级、一致快照并发、10^15 量级
big.Int 不溢出计算，以及与独立朴素模型的随机差分测试。

```bash
go test ./fedalloc -v      # 含每次操作的输入/输出/判定依据日志
go test -race ./fedalloc/
```

设计取舍、被放弃方案与复杂度论证见 `fedalloc/DESIGN.md`。

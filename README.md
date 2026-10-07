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

## 服务网格流量路由模块

`servicemesh/` 提供配置发布与请求分流：有序规则匹配（路径精确/段边界前缀、头精确/前缀/存在、多值头）、权重分桶、子集就绪检查、策略逐字段继承、发布期校验（权重/策略/遮蔽蕴含）、版本化原子替换与无锁并发读。

- 设计说明（关键取舍、被放弃方案、复杂度证明、本地验证）：`servicemesh/DESIGN.md`
- 测试：功能覆盖、与独立朴素模型的随机差分、并发可串行化（`-race`）、可验证复杂度探针与基准；日志打印每次操作的输入/实际输出/判定依据。

```bash
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath   # 若默认缓存目录只读
go test -v ./servicemesh
go test -race ./servicemesh
go test -bench=. -benchmem -run=^$ ./servicemesh
```

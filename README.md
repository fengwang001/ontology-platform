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

## 副本联邦分配器（`federation` 包）

多集群工作负载副本分配：最小保障、按权重分摊、上限饱和后逐轮再分配、
不可用/已删除集群迁出、全有或全无的可复现拒绝。全程 `big.Int` 运算，
支持 10^15 量级；并发安全，错误四分类且有严格优先级。

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/go-cache
go test -race -v ./federation/   # 14 个用例：并列打破、多轮饱和、零权重、
                                 # 迁出、冲突、缺口、极大值、顺序无关、
                                 # 错误优先级、并发、2 万组朴素模型对照、
                                 # 与 total 无关的性能计数证明
```

设计取舍、被放弃方案与本地验证步骤见 `docs/DESIGN.md`。

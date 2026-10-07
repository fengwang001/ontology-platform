# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：对象类型版本迁移与运行中实例双写回填

对象类型的结构变更在存量实例尚未全部回填完成期间，新旧两种版本的
读写请求都得到与「迁移已经完成」等价的正确结果。模块划分：

- `ontology/migration` — 对象类型版本声明与兼容性判定（矛盾校验、生效追踪、视图现算）
- `ontology/router` — 读写路由与一致性仲裁（实例存储、双版本读写、两阶段回填钩子）
- `ontology/backfill` — 存量实例异步回填（确定性顺序、竞争跳过）
- `ontology/naive` — 朴素参考模型，仅用于对照测试
- `ontology/difftest` — 随机操作序列下优化实现与朴素模型的逐条对照

设计取舍、被放弃的方案与本地验证方法见 [docs/DESIGN.md](docs/DESIGN.md)。

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

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

## 泛型实例化登记

核心 API 位于根包：

- `NewRegistry(Config{...})` 创建实例登记表，可配置总数、单定义数与深度上限。
- `RegisterDefinition` 登记泛型定义；`UpdateDefinition` 更新已登记定义并使依赖闭包过期。
- `Instantiate(def, args...)` 规范化实参、命中或新建实例，并通过 `Nested` 处理嵌套实例化。
- `Get` 查询有效或过期实例；`Cleanup` 删除过期且无活动依赖者的实例。
- `Summary` 在同一锁快照中返回有效数、过期数、每定义数、累计命中和累计新建。
- 设置 `Config.Logger` 可记录每条实例化输入、输出与 `created`/`hit`/错误码判定依据。

详细取舍见 [DESIGN.md](DESIGN.md)。验证命令：

```bash
go test ./...
go test -race -v ./...
go vet ./...
go test -run '^$' -bench='Benchmark(HitStable|UpdateOnly)' -benchtime=100x ./...
```

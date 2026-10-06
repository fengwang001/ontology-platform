# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分层配置版本化发布与解析

`layerconfig/` 提供四层（全局/环境/区域/实例）配置的登记、原子发布、叠加解析、
显式取消、按键合并、层级锁定、历史读取与回滚。

- 设计说明（关键取舍、被放弃方案、性能论证、本地验证）：`docs/design.md`
- 使用指南与 API 示例：`docs/usage.md`
- 可运行演示：`go run ./cmd/demo`
- 测试：功能用例、与独立朴素模型的大规模随机逐步对照、并发线性化（`-race`）、
  解析不随规模/版本增长与结构共享的可验证测试；操作输入/实际输出/判定依据均记录。

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

若 Go 不在 PATH（本机位于 `/usr/local/go/bin`）：

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache
```

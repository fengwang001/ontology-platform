# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology`：链接类型基数管理。支持运行期下调基数上限，超额链接按
  确定性规则进入待处理状态，可显式保留/删除或由默认路径清理；
  派生状态在待处理期间标记为暂时不可信。设计取舍见
  [docs/design.md](docs/design.md)。
- `cmd/server`：端到端演示（下调 -> 标记 -> 保留/清理 -> 审计轨迹）。

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

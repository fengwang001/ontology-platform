# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统

- `idx/`：增量索引子系统。消费对象属性变更流（允许乱序/重复），
  以 `(Version, ID)` 为合法串行顺序维护"按属性取值定位对象"的
  倒排索引；支持索引依据字段随类型版本迁移原子切换、失败整体
  回滚；并发可线性化；查询开销与累计事件总量无关。
  设计说明见 [docs/design.md](docs/design.md)。

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

# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统

- `ontology/` —— 双时态链接历史一致性审计：链接类型的版本化基数约束、
  创建/撤销的双时态（有效时间 + 记录时间）历史轨迹、任意历史记录时刻的
  镜像一致回放、按版本分段的历史违反审计、判定日志与并发线性化保证。
  设计取舍与验证方法见 [DESIGN.md](DESIGN.md)。
- `naive/` —— 独立朴素对照实现（线性扫描、无快照），用于差分测试。

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

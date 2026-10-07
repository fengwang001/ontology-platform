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

## 功能：权限固化导出与事后审计追溯

- `ontology/`：核心库。导出时把每个被排除属性命中的规则版本 ID 固化进导出结果；
  规则以不可变版本存储，历史导出与审计解析不受后续规则删除/覆盖/继承重组影响。
- `cmd/server/`：端到端演示（导出 → 规则删除与替换 → 历史审计追溯）。
- 设计取舍、被放弃方案与性能证明见 [DESIGN.md](DESIGN.md)。

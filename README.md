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

## 稀疏检出规则引擎（`sparse` 包）

给定提交的完整文件树与有序包含/排除规则（精确路径、目录前缀、本层通配），
判定哪些路径物化到工作区，并在提交或规则集变化时原子地计算
「新增物化 / 撤销物化 / 保持不变」三类集合，尊重本地修改（受阻整体拒绝，
可强制丢弃）。设计取舍与验证方法见 `docs/sparse-checkout-design.md`。

```bash
go test -race -v ./sparse/
```

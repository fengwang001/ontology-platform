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

## 生命周期计费器

访问期分层（热/凉/冷）、早离补费与月度免费检索额度的对象存储计费器位于
[`lifecycle/`](lifecycle/) 包，层级推导、存储费/补费/检索费公式、拒绝顺序与
本地验证方法见 [`lifecycle/README.md`](lifecycle/README.md)。

```bash
# 2000 组随机操作序列对照朴素模拟（带竞态检测，-v 查看输入/输出/判定日志）
go test -race -v ./lifecycle
```

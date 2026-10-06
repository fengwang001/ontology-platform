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

## 服务器名称证书选择器

`certselector` 包实现基于 SNI 主机名、客户端支持的密钥类型与当前时间的证书
选择，支持集合热更新、精确/通配/默认三类来源标记、无匹配/过期/密钥不支持
三类失败区分。选择开销不随证书总数线性增长（精确名哈希表 + 通配名反向标签
trie），并通过 1200 步随机操作序列与独立朴素模型对照验证。

- 设计与取舍：`certselector/DESIGN.md`
- 运行测试：`go test -race ./certselector/`
- 逐步判定日志：`CERTSELECTOR_LOG=/tmp/diff.log go test -run TestRandomDifferential -v ./certselector/`

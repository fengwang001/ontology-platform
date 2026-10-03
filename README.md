# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- [`hotrank`](hotrank/DOC.md)：滑动窗口分桶的实时热度排行榜，支持乱序/迟到
  计分事件、`M−⌊M/4⌋` 驻榜迟滞、并列名次（1、2、2、4）、相对上一榜的
  升降与掉榜；拒绝原因按 非法参数 > 已过期 > 时钟回退 > 得分溢出 报告。
  规则说明与本地验证方法见 `hotrank/DOC.md`。

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

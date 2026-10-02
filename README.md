# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包：`cron`

五字段 Cron 表达式触发时刻计算器与 CronJob 式错过触发判定控制器。
语法、日/周 OR/AND 规则、错过触发窗口、只补最近一次与三种并发策略
详见 [cron/README.md](cron/README.md)。

```bash
go test ./cron
go test -race -v ./cron
# 2000 组随机表达式差分测试，日志写入 cron/diff_test.log
go test -run TestDifferentialRandom2000 -v ./cron
```

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

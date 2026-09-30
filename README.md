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

## 日志截断位点一致维护器

`logtrunc` 包实现日志截断的位点一致维护：持久化位点只进不退、截断分
“先落标记再物理删除”两步、恢复时按标记与实际起始做崩溃双判定（一致即
干净、实际小于标记补删收敛、实际大于标记报告损坏）。详见
[docs/log-truncation.md](docs/log-truncation.md)。

```bash
go test -race -v ./logtrunc
```

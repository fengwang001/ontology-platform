# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统

- `fence/`：共享单车电子围栏还车判定与跨围栏调度服务（运营区/禁停区/奖励区嵌套判定、
  还车容量与计费、调度任务生成与认领、奖励每日限次）。设计取舍见 [DESIGN.md](DESIGN.md)。

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

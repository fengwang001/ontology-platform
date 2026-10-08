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

## eventloop：浏览器事件循环任务调度内核

`eventloop/` 实现由任务源队列、微任务检查点、渲染机会、空闲期与定时器
五部分协作的调度内核；时间只由注入时钟驱动（`AdvanceTo`），执行次序
唯一确定并输出带类型、句柄与时刻的轨迹。设计取舍见 `eventloop/DESIGN.md`。

```bash
go test ./eventloop/        # 定向测试 + 朴素模型随机对照
go test -race -v ./eventloop/
```

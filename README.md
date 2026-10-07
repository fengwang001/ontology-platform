# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：嵌套动作事务与校验钩子

- `txn` — 动作事务边界：暂存覆盖层、O(1) 快照、原子提交/回退
- `hooks` — 前置/后置校验钩子的注册与触发调度、全局触发日志
- `errors` — 错误归一化（参数非法 / 前置钩子失败 / 后置钩子聚合）
- `action` — 动作引擎：模式校验、嵌套调用、FIFO 串行执行器

设计取舍、被放弃的方案与验证方法见 [DESIGN.md](DESIGN.md)。

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
go test ./action
go test -run TestConformanceWithNaiveModel -v ./action

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

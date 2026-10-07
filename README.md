# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 并发控制

对象实例更新支持两种并存的并发控制方式，详见 [docs/design.md](docs/design.md)：

- 普通属性更新：乐观方式，依据版本号判定冲突；
- 动作触发的生命周期状态机转换：实例级独占占用权（非阻塞申请、
  租约 + 栅栏令牌、跨实例升序申请的死锁规避）。

核心代码位于 `ontology/` 包：

- `ontology/api.go` — 乐观更新、占用申请、快照读；
- `ontology/store.go` — 判定逻辑、租约、栅栏令牌、暂存-提交；
- `ontology/occupancy.go` — 占用句柄（Apply/Heartbeat/Commit/Abort）；
- `ontology/audit.go` — 审计日志（每次判定的输入、依据与结果，可重放）；
- `ontology/errors.go` — 互斥且可单独识别的拒绝码。

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

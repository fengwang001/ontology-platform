# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 在线状态聚合与订阅通知（`presence` 包）

多设备租约、聚合状态、隐身/拉黑可见性、惰性到期与订阅通知服务。
设计与取舍见 `presence/DESIGN.md`。

```go
svc, err := presence.New(60) // 固定租约 60 秒
err = svc.Report("alice", "phone", presence.StatusOnline, 100)
err = svc.Subscribe("bob", "alice", 101)
q, err := svc.Query("bob", "alice", 102)   // 可见状态 + 未到期设备数
d, err := svc.Drain("bob", 200)            // 变更通知 + 丢弃条数
```

操作：`Report` / `Offline` / `SetInvisible` / `Block` / `Unblock` /
`Subscribe` / `Query` / `Drain`。拒绝原因通过哨兵错误区分（如
`presence.ErrClockRollback`、`presence.ErrAlreadySubscribed` 等）。

验证：

```bash
go test ./presence -race
go test ./presence -run TestDifferential -v   # 1500 组随机差分（日志 presence/differential_run.log）
go test ./presence -bench . -run '^$'         # 扩展性基准
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

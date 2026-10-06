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

# 候补席位系统（standby 包）
go test ./standby
go test -race -count=1 ./standby
go test -run TestDifferentialRandom -v ./standby   # 2000 组随机序列 vs 朴素对照模型
go test -run TestPerfIndependence -v ./standby     # 规模 200/2000 的常数性验证

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 候补席位登记与兑现（`standby` 包）

设计与取舍见 `standby/DESIGN.md`（≤40 行）。接口在 `standby/types.go` 与 `standby/ops.go`：
`CreateFlight`、`Register`、`Withdraw`、`Confirm`、`ChangePriority`、`CancelConfirmed`、
`SetCapacity`、`CancelFlight`、`Snapshot`。

- 六态：候补中 / 待确认 / 已确认 / 已撤回 / 已过期 / 作废；三级优先；整组全有或全无。
- 过期按时序精确结算（同刻合并、链式兑现）；被拒绝的操作不结算、不推进时钟。
- 有序结构为 128 位定深二叉 Trie，登记/撤回/确认开销与队列长度、航班数无关。

# ontology-platform

本仓库包含 `netcode` 包：多人在线场景下的服务端权威输入仲裁、客户端预测与确认对账模块。

## netcode

- 服务端在 `Tick(now)` 中按玩家、按序号、按配额处理移动。
- 支持重复输入幂等、序号缺口拒绝、积压上限、时钟回退拒绝。
- 超步移动拒绝并消耗配额；越界移动截断后接受。
- 客户端立即预测，确认到达后回滚到权威位置并重放剩余未确认输入。
- 确认可乱序、重复或延迟到达；客户端只接受已处理序号更大的确认。

详细设计与取舍见 `docs/netcode-design.md`。

### 示例

```go
config := netcode.Config{WorldWidth: 100, Quota: 2, MaxStep: 10, Backlog: 16}
server, _ := netcode.NewServer(config)
_ = server.RegisterPlayer("player-1", 0)

server.Submit("player-1", netcode.Move{Sequence: 1, Delta: 5})
tick := server.Tick(1)
confirmation := tick.Confirmations["player-1"]

client, _ := netcode.NewClient(config, "player-1", 0)
client.AddInput(netcode.Move{Sequence: 1, Delta: 5})
client.ApplyConfirmation(confirmation)
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

当前环境若 `go` 不在 `PATH`，可使用 `/usr/local/go/bin/go`，并设置 `GOCACHE=/tmp/ontology-go-cache`。

### netcode 专项验证

```bash
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test ./netcode -run TestRandomInterleavingsAgainstNaiveModel -count=1 -v
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -race ./netcode
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test ./netcode -run '^$' -bench Benchmark -benchtime=1000x
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

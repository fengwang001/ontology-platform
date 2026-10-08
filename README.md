# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前包含：**车联网远程控车指令网关**（`internal/gateway`）——受理云端
发给车辆的控制指令，按车辆最新状态判定前置条件、互斥与唤醒配额，
管理下发、回执、过期与幂等。设计取舍与验证方法见
[docs/design.md](docs/design.md)。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行（HTTP 演示服务，默认 :8080）
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

### HTTP 接口（枚举均为字符串）

```bash
# 车端状态上报
curl -X POST localhost:8080/vehicles/v1/reports -d '{
  "seq":1,"time":100,"gear":"park","speed_kmh":0,
  "power":"sleep","lock":"all_locked","battery_pct":80}'

# 提交控制指令（type: unlock/lock/ac_on/ac_off/find_car/open_trunk/remote_start）
curl -X POST localhost:8080/vehicles/v1/commands -d '{
  "submitter":"app","request_id":"r1","type":"find_car",
  "time":110,"validity_sec":500}'

# 指令回执 / 查询指令 / 车辆快照
curl -X POST localhost:8080/vehicles/v1/commands/cmd-1/acks -d '{"time":130,"success":true}'
curl localhost:8080/vehicles/v1/commands/cmd-1
curl localhost:8080/vehicles/v1
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./internal/gateway
go test -run TestMutexMatrix ./internal/gateway

# 随机序列 vs 朴素模型对照（日志含输入、输出与判定依据）
go test -v -run TestRandomAgainstNaiveModel ./internal/gateway/

# 受理开销与历史无关的性能验证
go test -run TestAcceptanceCostIndependentOfHistory -v ./internal/gateway/
go test -bench BenchmarkSteadyState -benchtime 20000x -run XXX ./internal/gateway/

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

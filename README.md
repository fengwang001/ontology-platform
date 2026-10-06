# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## VRRP 风格主备状态机

`vrrp` 包提供无 I/O、无 goroutine 的单设备 VRRP 风格状态机。调用方注入单调毫秒时间、通告和操作事件；设备返回需要对外发送的通告，并可把这些通告喂给其他设备复现联合选主行为。

```go
device, err := vrrp.NewDevice(vrrp.Config{
	ID:               "device-a",
	Priority:         100,
	Preempt:          true,
	AdvertIntervalMS: 100,
})
if err != nil {
	return err
}

started, err := device.Start(0)
_ = started
_ = err

timeout, err := device.AdvanceTime(300)
_ = timeout
_ = err

adverts, err := device.TakeAdvertisements(300)
_ = adverts
_ = err
```

关键设计、取舍、被放弃方案、并发与 O(1) 论证见 [`vrrp/DESIGN.md`](vrrp/DESIGN.md)。

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
go test ./vrrp
go test -run TestMonitorTimeoutBoundaries ./vrrp

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

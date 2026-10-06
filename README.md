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

若系统缓存目录只读，可指定临时缓存（本仓库验证时使用）：

```bash
GOCACHE=/tmp/go-build-ontology PATH=/usr/local/go/bin:$PATH go test -race ./...
GOCACHE=/tmp/go-build-ontology PATH=/usr/local/go/bin:$PATH go test -bench BenchmarkSlotBookWithLargeHistory -run '^$' ./...
```

## 模块接口

- `booking.go`：搬家预约、取消、货梯签到/签离、爽约释放。
- `renovation.go`：装修申请、审批、延期、施工签到、投诉、暂停解除、退押。
- `quiet.go`：住户/货梯登记、静音分钟区间、法定节假日。
- `deposit.go`：共享押金充值、余额、逐笔流水与预占。
- `clock_heap.go`：按结束时刻确定性处理爽约与许可到期。
- `errors.go`：固定错误码及错误次序。

主要入口位于 `Service`：

- `BookMove` / `CancelBooking` / `BookingCheckIn` / `BookingCheckOut`
- `ApplyPermit` / `ReviewPermit` / `ExtendPermit` / `PermitCheckIn` / `PermitCheckOut`
- `AddComplaint` / `LiftSuspension` / `RefundPermit`
- `SetQuietRanges` / `AddHoliday` / `TopUp` / `Balance` / `Ledger`

所有时间均为整数分钟，装修日期用整数日，半开施工区间为 `[StartDay, EndDay)`。
错误只报告固定次序中的第一个：参数非法、时钟回退、不存在、时间窗口、状态、静音冲突、押金不足。

详细取舍见 `DESIGN.md`。

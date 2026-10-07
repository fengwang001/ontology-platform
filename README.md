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

## calendar 包：短租可订日历与预订约束

`calendar` 提供按整数日序号组织的房源日历与预订约束服务：

- 房东设置：按日最短入住、换客间隙、封锁区间（可缩短/延长）。
- 两阶段预订：保留（H 个时刻单位内可付款）→ 支付确认；过期自动失效。
- 可订性判定：封锁、预订冲突、换客间隙、最短入住、孤夜五类子原因；
  判定开销与历史预订总数无关（夜级哈希 + 有界邻居扫描）。
- 取消退款三档阶梯（>=P 全额 / >=Q 半额 / 其他不退），已确认预订可改期一次。
- 全部操作可并发调用，全局锁保证等价于某个串行顺序；被拒绝的操作不留痕。

设计取舍见 `calendar/DESIGN.md`，示例：

```go
s, _ := calendar.NewService(24, 7, 3) // H=24, P=7, Q=3
s.AddListing(0, "apt-1", 100, 1, 0)   // now, ID, 每晚价, 最短入住, 间隙
id, _ := s.Hold(0, "apt-1", 10, 13)   // 保留 [10,13) 三夜
s.Pay(1, id)                          // 支付确认
refund, _ := s.Cancel(2, id)          // 距入住 8 天 >= P，全额退款
```

测试：`go test ./calendar/ -race -v`（含随机差分对照日志）。

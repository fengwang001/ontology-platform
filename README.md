# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

本仓库当前包含一个独立子系统 `railway`：铁路列车区段席位复用与分站票额系统。
详见 [DESIGN.md](DESIGN.md)。

## railway 子系统

一趟列车按顺序停靠若干站，同一座位可在不重叠（左闭右开）区段上被先后复用；
各发站对各到站有分配票额，预售截止后整站剩余票额不可逆并入共用票额；
座位售罄后可在每相邻区段按 `floor(座位总数*比例)` 的上限出售无座票。

- 选座：两端相接 > 仅一端相接 > 不相接，同级按车厢号、座位号，结论唯一。
- 票额：先扣 OD 分配票额，为零再扣共用；并入后统一扣共用。
- 退票：座位/无座名额释放，票额退回当初来源（来源已并入则退共用），不自动升舱无座票。
- 时钟：变更操作时刻单调不减；被拒绝操作不推进时钟、不改任何状态。
- 拒绝次序：参数非法 > 时钟回退 > 列车不存在 > 车票不存在/已退 > 已发车 > 乘车人相交 > 票额不足 > 无席位。

入口 API（`service.go`）：

- `NewService(Config{AdvanceSeconds, StandingRatio})`
- `(*Service).AddTrain(TrainSpec)`：发车时刻非负严格递增，`Alloc[from][to]` 为 OD 分配票额。
- `(*Service).Buy(now, trainID, TicketDesc{From,To,Passenger}, wantStanding) (*BuyResult, error)`
- `(*Service).Refund(now, ticketID) error`
- `(*Service).Remaining(now, trainID, from, to) (QuotaView, error)`

错误统一为 `*OpError`，其 `Reason` 字段给出上述拒绝类别；成功购票的
`Ticket.Standing` 区分有座/无座，`Ticket.QuotaShared` 标识票额来源。

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

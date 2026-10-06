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

## 合租费用分账与净额清算服务（`ontology` 包）

见 `ontology/DESIGN.md`（关键取舍与放弃方案）。核心 API（均为并发安全方法）：

- `NewService(maxAmount, disputeWindow D)`：创建服务。
- `CheckIn(now, id, room, checkInDay, area)` / `CheckOut(now, id, outDay)` /
  `ChangeRoom(now, id, newRoom, day, area)`：入住、退出（左闭右开，退出日不在住）、换房（换房日按新房间面积）。
- `EnterBill(now, id, amount, startDay, endDay, method, payerID, landlordCollect)`：
  录入共同账单；`method` 为 `SplitPerHead` 或 `SplitByArea`；`landlordCollect=true` 表示房东代收。
- `RaiseDispute(now, billID)`：录入后 `D` 天内争议一次，立即从净额剔除。
- `Adjudicate(now, billID, newAmount)`：裁定（`newAmount <= 原金额`）后按新金额重算并生成补充清算。
- `NetBetween(a, b)`：两名住户当前净额（正表示较小 ID 一方应收）；单次哈希读取，O(1)，与账单数无关。
- `Settlements(id)`：原清算与各次补充清算记录；`LandlordShare(billID)`：账单中房东承担份额。

错误按固定次序只返回第一个：参数非法 `ErrInvalid`、时钟回退 `ErrClockBack`、
不存在 `ErrNotFound`、在住期重叠 `ErrOverlap`、状态不允许 `ErrState`、金额越界 `ErrAmount`。

测试除覆盖开闭区间、交接当天、无人日归属、余数次序、净额反转、退出/补充清算边界、
争议裁定重算、换房当天面积、拒绝次序与不留痕、守恒外，还以独立朴素模型对
60 组随机操作序列逐步对照（`go test -v` 打印每步输入/输出/判定依据），并含并发、
竞态检测与确定性重放。

## 代码检查

```bash
gofmt -l .
go vet ./...
```

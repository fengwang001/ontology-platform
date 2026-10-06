# ontology-platform

分区停车场预约、核销、超时改派与候补服务，Go 1.26+。

## 环境要求

- Go 1.26+（`go version` 确认）。
- 无第三方依赖。

## 本地验证

```bash
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache go test ./...
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache go test -race -v ./...
PATH=/usr/local/go/bin:$PATH go vet ./...
```

## API

- `NewService(zones, spots)`：初始化分区、车位和费率。
- `Reserve(at, req)`：可用时立即锁定具体车位。
- `RegisterWait(at, req)`：目标时段无位时登记候补。
- `CheckIn(at, id)`：窗口内核销，必要时被动改派。
- `Cancel(at, id)`：未核销预约取消。
- `Leave(at, id)`：离场并冻结超时费与赔付。
- `SpotOccupantAt(spotID, at)`：按秒查询车位归属。
- `Reservation(id)` / `Fee(id)`：预约快照与费用明细。

## 模块

- `types.go`：错误码、配置、预约和费用类型。
- `interval_index.go`：每车位活跃/归档 AVL 区间树。
- `waitlist.go`：自动到期、到期堆和候补提升。
- `lifecycle.go`：预约、核销、取消、离场、改派和选位。
- `service.go`：线程安全服务入口、构造与只读查询。

关键取舍、放弃方案、复杂度与可复现性证明见 `DESIGN.md`。随机对照测试逐条记录输入、输出、错误码和判定依据。

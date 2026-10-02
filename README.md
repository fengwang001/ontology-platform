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

## 带负载上限的一致性哈希环

实现在 `boundedhash/ring.go`，负载系数为 `c = Cnum/Cden`，要求 `1 ≤ Cden ≤ Cnum ≤ 10^6`。

- `Put`：放置前已有 `K` 个键、环上有 `N` 个节点时，本次容量为 `ceil(Cnum*(K+1)/(Cden*N))`。在已排序点序列中二分找到第一个位置不小于 `pos` 的点，再顺时针顺延；到达最大点后回到最小点。负载等于容量的节点视为已满，会跳过该节点的所有点，直到遇到负载小于容量的节点。
- `AddNode`：节点 id 必须为正整数；每节点 1 到 64 个点，点在全环范围内且节点自身内均不可重复。新增节点只登记点，不迁移任何已有键。
- `Delete`：只把键从所属节点移除并令该节点负载减一，不触发迁移；已领取的 `seq` 不回收。
- `RemoveNode`：先移除节点的点并取出其全部键，再按这些键的 `seq` 升序重放置。设已重放前当前总键数为 `Kc`，每次容量独立计算为 `ceil(Cnum*(Kc+1)/(Cden*(N-1)))`；每成功重放一个键，`Kc` 递增。最后一个节点只有在没有键时才允许移除。
- `Rebalance(limit)`：容量固定为 `capR = ceil(Cnum*K/(Cden*N))`，其中 `K` 是调用开始时的总键数，不使用 `K+1`。按节点 id 升序处理；每个超额节点反复摘取其名下 `seq` 最大的键，先让原节点负载减一，再从该键原始 `pos` 起跳过负载不小于 `capR` 的点进行重放置。迁移按发生顺序返回；达到 `limit` 立即整体停止，同时返回剩余超额。

所有键只有 `Put` 成功时领取从 1 开始的 `seq`；删除、节点移除和再平衡都不改变 `seq`。所有公开方法都用同一把锁串行化临界区，查询使用读锁，因此并发调用等价于某个合法的串行顺序。

放置起点使用标准 lower-bound 二分；内部非导出计数器记录二分循环中的点比较次数，顺延扫描已满节点不计入。测试在 `P=100` 与 `P=10^5` 两档验证比较次数不超过 `ceil(log2(P))+1`（本实现分别不超过 8 与 18）。

测试包含题目示例、向上取整、回绕、同位置键、整圈跳过满点、节点移除时递增 `Kc`、再平衡取最大 `seq`、limit 截断、拒绝操作无副作用、并发竞态检测，以及 2000 组固定随机种子序列与逐步朴素模型的逐项对照。

### 本地验证

```bash
# 若 PATH 中没有 go，可使用：export PATH=/usr/local/go/bin:$PATH
GOCACHE=/tmp/go-build-ontology go test ./boundedhash -v
GOCACHE=/tmp/go-build-ontology go test -race ./boundedhash
GOCACHE=/tmp/go-build-ontology go vet ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

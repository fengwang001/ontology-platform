# ontology-platform

## NUMA 相位公平组队读写锁

根包提供 `numa.TeamRWMutex`，构造参数为：

- `Nodes`：NUMA 节点数 `M`，范围 1–8。
- `Threads`：线程数 `T`，范围 1–32。
- `NodeOf`：每个线程所属节点，长度必须为 `T`，节点号范围 0–`M-1`。
- `LocalLimit`：同一写组内连续本地传递上限 `B`，范围 1–16。

非法构造整体返回 `numa.ErrInvalidConfig`。所有 `RLock`、`WLock`、解锁、降级、升级和撤销调用由同一把互斥锁串行化，因此并发调用等价于某个确定的串行顺序。

### 相位与队列

- 空闲相位没有持有者，也没有等待队列；读相位至少有一个读者；写相位恰有一个写者。
- 读相位中没有写者等待时，新读者立即成批进入；已有写者等待后，新读者进入全局 FIFO 队列 `Qr`。
- 写者进入所属节点的 `Qw[n]`，非当前写组节点第一次有等待者时加入节点轮转队列 `G` 尾，`G` 中节点不重复。
- 最后一个读者离开时进入“起写”：取 `G` 队首节点和该节点 `Qw` 队首线程；`G` 空则回到空闲。

### 公平与本地传递

写者释放时严格按以下顺序判定：

1. `Qr` 非空：先按 FIFO 授予全部等待读者；若当前写组节点仍有写者，再把该节点排到 `G` 尾。
2. 否则，当前写组节点仍有写者且 `G` 为空或 `p < B`：在节点本地交给队首写者，`p++`。
3. 否则，当前写组节点重新排到 `G` 尾，再从 `G` 队首起写；跨节点起写会把 `p` 重置为 0。

因此等待读者总是先于本地传递；`G` 为空表示没有其他节点竞争，本地传递不受 `B` 限制。

### 降级、升级与撤销

- `Downgrade`：写者转为读者，授予顺序为原写者后接 `Qr` 全体；当前写组节点仍有写者时排入 `G` 尾。
- `Upgrade`：仅当调用者是读者且全局读持有集合恰为 `{t}` 时成功；成功后其节点若在 `G` 中则移出，但该节点 `Qw` 保留为本地写队列。
- `Cancel`：读等待者从 `Qr` 移除；写等待者从所属 `Qw` 移除，若该节点队列变空则同步移出 `G`。
- 读相位中撤销最后一个写等待者时，`Qr` 全体立即按序授予。

拒绝原因按“线程号越界 → 线程状态不符 → 升级冲突”的顺序只返回第一个；被拒绝的调用不改变任何状态。

### 可复现实验

随机测试使用固定种子生成 2000 组配置和调用序列，并由一个独立的朴素切片模拟逐步执行。每次调用都会在 `-v` 日志中记录输入、返回授予集合和判定依据，同时比较完整快照：相位、`R`、`w`、`gown`、`p`、`Qr`、全部 `Qw`、`G` 与线程状态。

```bash
go test -run TestRandomSequencesMatchNaive -v ./...
go test -race ./...
```

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

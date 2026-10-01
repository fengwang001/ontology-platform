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

## 推测构建队列

`NewCoordinator(B, Cap)` 创建协调器：`B` 是同时处于构建窗口的位置数，`Cap` 是队列容量。`Enqueue(id)` 总在队尾追加，位置从 1 开始，新项初始 `epoch=1`；只有位置不超过 `B` 的项可以上报构建结果。

- **前缀合并**：位置 `p` 的项报告通过时，位置 `1..p` 按位置顺序一起合并；这是因为该项的构建基线已经包含前面全部排队变更。合并后这些 id 永久进入已合并历史，剩余项位置前移但 `epoch` 不变。
- **失败剔除**：位置 `p` 的项报告失败时只剔除该项，原位置 `p+1..` 的全部后继项（包括窗口外等待项）`epoch` 加 1。位置 `p` 之前的项不受影响。
- **主动撤出**：`Dequeue(id)` 的队列变化与失败相同。被剔除或撤出的 id 之后可以重新入队，并重新从 `epoch=1` 开始。
- **epoch 含义**：每次前驱被失败或撤出，后继项就换一代。迟到报告的 epoch 与当前 epoch 不一致时被拒绝，旧结果不能影响新队列。
- **错误优先级**：构造先校验 `B < 1`，再校验 `Cap < 1`；入队依次为空 id、已合并、已在队列、队列已满；上报依次为 id 不在队列、位置超出构建窗口、epoch 过期；撤出仅在 id 不在队列时拒绝。被拒绝的操作不改变任何状态。
- **并发与复现**：所有方法由同一个互斥锁串行化，因此并发调用等价于某个合法串行顺序。相同操作序列重放会得到相同结果；同一 `(id, epoch)` 的并发通过上报恰好一次成功。

测试包含固定边界用例、32 路并发通过上报、以及 2000 组由固定种子生成的随机操作序列。随机测试使用独立朴素模拟逐步对拍，在 `-v` 日志中打印每步的输入、双方输出和判定依据：

```bash
go test -race -v ./...
go test -run TestRandomSequencesAgainstNaiveSimulation -v ./...
```

# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 通知投递调度器

核心实现位于 `scheduler.go`，构造函数为 `NewScheduler(off, qs, qe, G, Cap, M)`。

- 本地分钟使用非负余数：`lm(t) = ((t + off) mod 1440 + 1440) mod 1440`。
- 本地日序号向负无穷取整：`dy(t) = floor((t + off) / 1440)`，负偏移下也保持该规则。
- `qs < qe` 时静默区间为 `[qs, qe)`；`qs > qe` 表示跨午夜，即 `lm >= qs` 或 `lm < qe`；二者相等表示无静默。
- `shift(t)` 只在静默时推进到静默终点；终点不含，因此 `lm=qe` 不再顺延。
- 普通通知先计算 `now + G`，再对结果静默顺延，不能先顺延再加 `G`；`G=0` 时同样处理。
- 批次以 `(f, seq)` 放入最小堆。到期且 `sent[dy(f)] < Cap` 时投递并移除；否则改成 `shift((dy(f)+1)×1440-off)`，保留原 `seq` 后重新入堆。
- 每日上限与静默形成级联：批次可从当前日顺延到下一本地日零点，若该零点仍在静默时段，再由 `shift` 推到静默终点；一次到期处理可连续跨多个已满自然日。
- 紧急通知在到期处理之后立即投递并计入 `sent[dy(now)]`，即使当日已达 `Cap` 也不拦截，但仍占用后续普通批次的上限名额；紧急通知不创建待投批次。
- 重要通知始终创建单条批次，不进入同 key 普通批次索引；普通通知只在到期处理后仍存在同 key 普通批次时并入。
- 新创建批次即使 `f <= now` 也不会在本次操作投递，必须等下一次 `Submit` 或 `Poll` 的到期处理。
- 拒绝顺序为构造参数、空 key、非法优先级、非法时间、时钟回退、队列满；被拒操作不留下可观察状态变化。
- 所有公共操作由同一把互斥锁串行化；重放同一操作序列会得到相同的投递时刻、条数和剩余批次。
- `dueProbes` 是非导出计数器，每次查看堆顶加一，单次操作满足“到期批次数 + 顺延次数 + 1”，与队列总长度无关。

### 本地验证

```bash
# 当前环境若未把 Go 放入 PATH，可显式使用 /usr/local/go/bin/go。
GOCACHE=/tmp/ontology-gocache go test -v ./...
GOCACHE=/tmp/ontology-gocache go test -race ./...
GOCACHE=/tmp/ontology-gocache go vet ./...
```

`TestRandomDifferential` 使用固定随机种子重放 2000 组操作，并将每组配置、每个输入、实际输出、朴素逐分钟模型输出和判定依据写入 `-v` 日志。

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

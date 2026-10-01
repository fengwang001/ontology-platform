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

## 死信重放器（`dlq` 包）

`dlq` 提供并发安全的命名队列 + 共享死信区系统，核心语义：

- **入队序号 oseq**：`Enqueue` 成功时分配，从 1 起全局递增，终身不变；队列已满或不存在则拒绝。
- **死亡序号 dseq**：`SendToDead` 把队首消息移入死信区时分配，从 1 起按进入先后递增；队空或队列不存在则拒绝。同一消息再次进入死信区时分配**新的** dseq，oseq 与累计重放次数保持不变。
- **重放顺序**：`Replay(queue, n)` 取死信区中属于该队列且重放次数小于上限 R 的条目，按 **oseq 升序**（而非死亡序 dseq）依次追加到目标队列队尾。
- **次数上限**：单条消息累计重放次数达到 R 后被跳过，留在死信区，且**不占用** n 的名额；跳过后继续取后面的条目。
- **部分成功**：目标队列容量 C 中途耗尽时，已放回的条目不回滚，其余留在死信区；只要放回至少一条即成功并返回放回条数。

### 整体拒绝（不改变任何状态），原因可区分、优先级自上而下

| 优先级 | 情形 | `RejectReason` |
| --- | --- | --- |
| 1 | n < 1 或队列名为空 | `invalid_argument` |
| 2 | 队列不存在 | `queue_not_found` |
| 3 | 死信区中没有该队列的条目 | `no_dead_entries` |
| 4 | 有条目但全部已达重放上限 | `all_exhausted` |
| 5 | 目标队列已满致一条也放不进 | `queue_full` |

错误类型为 `*dlq.RejectError`，可用 `errors.Is(err, dlq.ErrRejected)` 与 `errors.As` 取出 `Reason` 判定。

### 并发与确定性

所有操作（入队、出队、送入死信、重放、查询）由单把互斥锁串行化，并发调用结果等价于某个串行顺序；每条消息恰好处于“队列中、死信区”之一或已被出队；相同调用序列重放得到完全相同的队列与死信区内容。

### 本地验证

```bash
# 全部测试（含与朴素逐步模拟的 3000 步随机对照，日志打印输入/输出/判定依据）
go test ./dlq/ -v

# 竞态检测
go test -race ./dlq/

# 指定边界用例
go test ./dlq/ -run 'TestReplayOrderByOSeqNotDSeq|TestReplaySkipsExhausted|TestReplayPartial|TestReplayQueueFull|TestReDeath|TestRejectReasons' -v
```

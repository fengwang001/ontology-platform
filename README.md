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

## 多服务台单队列仿真器

`queue` 包提供离散事件排队仿真器。创建时指定 1 到 64 个服务台：

```go
sim, err := queue.New(3)
err = sim.Add(queue.Customer{ID: 1, Arrive: 0, Service: 4, Patience: 2})
err = sim.AdvanceTo(10)
outcome, err := sim.Outcome(1)
stats := sim.Stats()
```

初始水位为 `-1`。`AdvanceTo(t)` 只跳转并处理 `(H, t]` 中存在事件的整数时刻；每个这样的时刻严格按以下顺序执行：

1. **完成**：释放本时刻服务结束的服务台；随后按服务台编号升序，每个空闲台从队首取顾客。
2. **到达**：本时刻到达的顾客按 `ID` 升序处理；有空闲台时立即分配编号最小的空闲台，否则进入公共 FIFO 队尾。
3. **放弃**：队列中所有 `arrive + patience <= 当前时刻` 的顾客，按队列次序离队。

服务边界为半开区间 `[start, start+service)`：顾客在 `start+service` 时刻完成并释放服务台。队首顾客若在服务台释放时刻的放弃时刻恰好等于当前时刻，即 `start == arrive + patience`，会在“完成”阶段先开始服务，属于已服务；“放弃”阶段只能移除仍在队列中的顾客。顾客到达时若耐心为 0 且没有空闲台，会在“到达”阶段立即放弃，不入队，因此不会增加最大队长。

事件来源包括到达时刻、服务完成时刻和队列顾客的放弃时刻。同一时刻多个到达只按 `ID` 排序，与 `Add` 调用次序无关；把同一次推进拆成任意非递减的多次 `AdvanceTo`，最终每位顾客的 `Outcome` 与汇总 `Stats` 均与一次推进到末尾完全相同。所有提交、推进和查询方法都由互斥保护，结果等价于某种合法串行顺序。

`Add` 的错误按优先级区分为参数非法、ID 重复、到达时刻不晚于当前水位；`AdvanceTo` 拒绝负数或回退水位；查询未知 ID 返回 `ErrCustomerNotFound`。被拒绝的调用不会修改水位、队列、顾客状态或统计。

### 本地验证

```bash
# 全量测试；包含 2000 组随机场景与逐整数时刻朴素模拟对照
go test -v ./queue

# 竞态检测
go test -race ./queue

# 若 Go 构建缓存位于只读目录，可显式指定可写缓存
GOCACHE=/tmp/ontology-go-cache go test -race -v ./queue
```

随机测试使用固定种子；`go test -v` 会打印每个场景的输入顾客、推进切分点、输出状态/统计以及判定依据。

## 代码检查

```bash
gofmt -l .
go vet ./...
```

# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## NVIC 中断模型

`nvic.Controller` 是可并发调用的嵌套向量中断控制器模型。构造函数 `New(M, s)` 要求 `M >= 1` 且 `0 <= s <= 7`；每个中断都有使能、挂起、活动标志和一个 8 位优先级，数值越小越紧急。

- 优先级分组：组优先级为 `p >> s`，子优先级为 `p` 的低 `s` 位；屏蔽阈值为 `b`，`b=0` 不屏蔽，`b>0` 时屏蔽所有 `g(p) >= g(b)` 的中断。
- 候选选择：在已使能、已挂起、未活动且未被屏蔽的中断中，按 `(组优先级, 子优先级, 编号)` 字典序选择最小值。
- 抢占判定：当前运行级是运行栈顶中断的当前组优先级，栈空时为无穷大；候选只有在组优先级严格小于当前运行级时才立即进入。同组不同子优先级不抢占。
- 进入事件：清除挂起、置位活动并压栈；线程态产生非抢占 `Enter`，栈非空时该 `Enter` 标记为抢占。
- `Return()`：先产生 `Exit` 并弹出、清除活动；若候选严格高于新栈顶，或栈空且存在候选，则产生 `TailChain` 并直接进入。否则栈非空产生 `Resume(新栈顶)`，栈空产生 `Idle`。
- 活动中断可以再次 `Pend`；它退出后若成为候选，可立即尾链进入，包括再次进入自己。
- 非法构造、编号、优先级、阈值或空栈 `Return()` 返回独立哨兵错误；校验失败不会改变任何状态。

测试中的朴素模拟器独立按上述逐步规则维护状态，随机操作会逐条打印输入、事件输出、候选、当前运行组、阈值组和运行栈，便于复现判定依据。

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

如果当前环境没有可用的 Go 缓存目录，可显式指定：

```bash
GOCACHE=/tmp/ontology-go-cache go test -race -v ./nvic
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

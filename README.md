# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## deadlock 包：多实例资源分配图死锁检测与最小代价消解

`deadlock` 实现带备选请求的多实例资源分配器，所有方法可并发调用
（互斥锁串行化，`Resolve` 整体是一个原子步骤）。

### 备选授予规则

- `Request(p, alts)` 携带 1..3 个备选向量（任选其一）。按列出顺序取
  **第一个** 满足 `alt[r] <= avail[r]`（对所有 r）的备选授予——是第一个
  可满足的，而不是“最合适”的；全部放不下才阻塞，不做部分授予。
- 阻塞时记录整组备选与全局阻塞序号 `bseq`（初值 0，每次阻塞加一后赋给
  该进程，授予与被拒绝的操作都不耗序号）。
- 校验顺序只报第一个原因：`Request` 为 参数非法 → 进程不存在 → 已阻塞 →
  永不可满足；`Release` 为 参数非法 → 进程不存在 → 已阻塞 → 归还超过持有。
  被拒绝的操作不改变任何状态与 `bseq` 计数器。
- “永不可满足”指**全部**备选都不可满足（某备选存在 r 使
  `alloc[p][r]+alt[r] > T[r]` 时该备选不可满足）；只要还有一个备选原则上
  可满足，请求即被接受（授予或阻塞）。恰等于 T 允许，大 1 拒绝。

### 授予不动点

`Release` 与 `Resolve` 的每次回滚之后，反复在全部阻塞进程中选 `bseq`
最小且存在可满足备选的进程，授予其第一个满足的备选并解除阻塞，直到没有
这样的进程。因此后阻塞的小请求可以越过先阻塞的大请求（按 `bseq` 找第一个
可满足者，而非严格队首），且一次释放可能触发连锁授予。不变量：任何操作
结束后不存在“某备选不大于 avail”的阻塞进程。

### 归约判定与净增量推导

`Detect` 做图归约：令 `Work = avail + Σ 未阻塞进程的 alloc`（未阻塞进程
视为总会结束并释放）。反复按进程编号升序检查不在 Finish 的阻塞进程，只要
它存在某备选不大于 Work，就把它加入 Finish 并令 `Work += alloc[p]`——
它取得备选后结束，备选与持有量一并归还，净增量恰为持有量 `alloc[p]`。
不再变化时，不在 Finish 的阻塞进程即死锁集合。归约检查阻塞进程的次数
（非导出计数器 `checks`）不超过 `b(b+1)/2`（b 为阻塞进程数），不是枚举
完成顺序；测试中与枚举全部完成顺序的朴素实现对照 2000 组随机序列。

### 牺牲者选择规则

`Resolve` 反复执行：计算死锁集合，为空则结束；否则在集合中**持有量总和
大于 0** 的进程里选代价 `Σ alloc[p][r]*c[r]*(1+rb[p])` 最小者（并列取
小编号）为牺牲者。牺牲者被回滚：持有量全部并入 `avail`、阻塞请求被取消、
`rb[p]` 加一；若加一后 `rb[p] == L` 则永久中止（此后不存在），否则仍存活
可再次 `Request`。随后做一次授予不动点，再重新检测。返回每步
`(牺牲者, 代价, 授予列表, 是否永久中止)`。回滚次数越多代价越高，因此先前
便宜者被回滚后可能不再是最小代价者。

### 本地验证

```bash
go test ./deadlock/            # 全部单元测试 + 2000 组随机对照
go test -race ./deadlock/      # 竞态检测
go test -v ./deadlock/ -run TestRandomAgainstNaive  # 查看随机序列的输入/输出日志
```

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

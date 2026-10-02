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

## 批写入隔离器（`./isolation`）

`isolation` 包实现带二分隔离与已知毒丸表的批写入隔离器。sink 是注入的
`Write(ids) error`，对一批编号整批原子地成功或失败。

### 错误分类与重试

- 满足 `errors.Is(err, ErrTransient)` 的错误为瞬时错误：同一批最多尝试
  R+1 次（构造参数 R ∈ [0,10]），任一次成功即整批交付。
- 其余非 nil 错误为永久错误：立即停止重试，记 `perm=true`。
- R+1 次全部为瞬时错误记 `perm=false`；单条记录在此情形下以
  `Exhausted` 进入死信，且不写入已知毒丸表。

### 二分与右半推断

永久失败的批被拆成左半（前 `ceil(n/2)` 个）与右半，先递归处理左半，再
处理右半。推断规则：若本批已确认永久失败（`perm=true`，含 `certain`
传入）且左半整体交付成功（`lok=true`，瞬时重试后成功也算），则右半必然
含有毒丸，右半以 `certain=true` 递归——不再对右半整批调用 sink，直接
继续拆分。

成立前提：sink 对整批原子成功/失败，且永久失败当且仅当批内含有毒丸
记录。因此"左半全部干净且整批仍永久失败"蕴含"右半含毒丸"。
`certain=true` 的单条记录不再调用 sink，直接判 `Poison`。

### 已知毒丸表

- 容量 Km ∈ [0,1000]，Km=0 表示不记录。
- Submit 开始时快照读取一次：已在表中的编号以 `Known` 摘出，不产生
  sink 调用。
- Submit 结束时把本次确认的毒丸按判定顺序整体并入：表满时淘汰最早追加
  者；已在表中的条目不改变位置；并发 Submit 之间互不可见新入表者。
- `Known()` 返回当前表内容（按追加先后）。

### 预算

单次 Submit 的 sink 调用预算为 Cmax ∈ [1,10^6]。每次调用 sink 前检查：
已达 Cmax 次则整个 Submit 中止，当前批与所有尚未裁决的记录（含待处理
的右半）以 `Budget` 进入死信；已裁决的交付与死信保持不变，中止前已确认
的毒丸仍写入已知毒丸表。

### 调用次数上界

无瞬时错误、无已知毒丸、预算足够时，对 n 个编号、k 个毒丸（k ≥ 1）：

```
calls ≤ 1 + 2·k·ceil(log2 n)
```

推导：无瞬时错误时每个被调用的节点恰好调用一次。根节点 1 次。沿每个
毒丸到根的路径，每一层至多产生 2 次调用——路径上的子节点一次（失败或
最终的毒丸叶子），其干净兄弟子树一次（整批成功）；由右半推断规则，
`certain` 节点不调用，只会减少次数。路径长度不超过 `ceil(log2 n)`，k
个毒丸的路径合计至多 `2·k·ceil(log2 n)` 次，加根节点即得上界。取等
情形：n=1024、k=1 时，毒丸在最左位置恰为 21 次（每层兄弟都需调用），
最右位置恰为 11 次（右半逐层被推断为 certain）。

### 本地验证

```bash
go test ./isolation/                 # 全部用例（含 2000 组随机对照）
go test -race -v ./isolation/        # 竞态检测 + 打印每组输入/输出/判定依据
go test -run TestCallCounts1024 ./isolation/   # n=1024 两档调用次数与上界
```

随机对照测试将隔离器与按规格逐条写成的朴素递归模拟在 2000 组随机批、
毒丸分布与瞬时错误脚本（含紧预算、小容量表、多轮 Submit）下对比交付
清单、死信清单、Calls、sink 调用序列与最终已知毒丸表，并校验每条
Submit 的交付/死信不相交、并集为入参、保序、交付恰好一次成功调用、
Poison 从未出现在成功调用中等不变式。

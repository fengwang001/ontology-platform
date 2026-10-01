# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 属性访问内联缓存站点管理器（`ontology` 包）

全局方法表把形状编号（正整数）映射到目标，多个按名字创建的访问站点各自维护
至多 `M` 个（形状，目标）缓存条目。实现见 `ontology/inlinecache.go`。

### 状态迁移

站点状态由缓存条目数决定（多态上限 `M >= 2`，构造时校验）：

- 0 个条目：`empty`（空）
- 1 个条目：`monomorphic`（单态）
- 2..M 个条目：`polymorphic`（多态）
- 第 M+1 个**不同**形状访问未命中：清空全部条目并进入 `megamorphic`（超多态），
  **一旦进入永不退出**（重定义也不回退）

### 访问规则 `Access(site, shape)`

- 站点已超多态：直接查全局方法表返回，计 1 次超多态访问，**既不算命中也不算未命中**。
- 否则命中缓存条目：返回缓存目标，计 1 次命中。
- 否则未命中：查全局方法表；形状已定义时计 1 次未命中——
  - 加入前条目数已为 `M`：清空缓存、进入超多态（本次仍计未命中并返回表中目标，
    第 M+1 个形状不进入缓存）；
  - 否则把（形状，目标）加入缓存，条目数加一。

### 统计口径

每个站点独立维护 `hits` / `misses` / `megamorphic` 三个计数器，
恒有 `hits + misses + megamorphic == 该站点成功访问总次数`（被拒绝的访问不计数）。

### 定义与精确失效 `Define(shape, target)`

- 形状首次定义：只写方法表，不影响任何站点。
- 已定义且目标不同（重定义）：更新方法表，并从**所有缓存了该形状的非超多态站点**
  删除该条目；状态随条目数回落（多态→单态→空）。超多态站点跳过，不回退。
- 已定义且目标相同（`reflect.DeepEqual`）：无任何影响。

### 拒绝原因（整体拒绝，不改变任何站点、统计与方法表）

| 错误码 (`ErrorCode`) | 触发条件 |
| --- | --- |
| `invalid_limit` | `NewManager` 的 `M < 2` |
| `invalid_shape` | `Define` / `Access` 的形状不是正整数 |
| `site_exists` | `CreateSite` 时名字已存在 |
| `site_not_found` | `Access` 时站点不存在 |
| `shape_undefined` | `Access` 时形状未在方法表定义（不改变站点与计数） |

错误均为 `*ontology.OpError`，可用 `ontology.ErrorCodeOf(err)` 提取可区分原因。

### 并发与可复现性

所有创建/定义/访问/查询由同一把 `sync.RWMutex` 保护（查询用读锁），
并发结果等价于某个串行顺序；操作无随机、无 map 迭代泄漏到可观测结果，
相同操作序列重放得到完全相同的状态与统计。

### 本地验证

```bash
# 场景测试（含每条操作的 输入/输出/判定依据 日志）
go test -v ./ontology

# 竞态检测：并发混合操作 + 全部用例
go test -race ./ontology

# 朴素模型差分对照（固定种子随机脚本，逐步比较输出、状态与统计）与重放确定性
go test -run 'TestNaiveDifferential|TestReplayDeterminism' -v ./ontology
```

测试覆盖：恰好 M 个形状仍为多态、第 M+1 个形状进入超多态且该次计未命中、
超多态后重定义不回退、重定义使多态回落单态再回落空、相同目标重定义不失效、
未定义形状拒绝且无副作用、各类可区分拒绝原因、被拒操作零状态变更，
并与独立编写的朴素参考模拟器逐步对照。

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

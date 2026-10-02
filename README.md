# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 有序访问控制列表

`ontology.New(D, K)` 创建访问控制树。`D` 是最大深度（1～64），`K` 是单节点显式 ACE 上限（1～64），根节点固定为 `/`、容器、深度 0。

公开类型与操作：

- `ACE{Allow, Principal, Mask, Flags}`：`Principal` 为非空字节串，`Mask` 为 1～65535，`Flags` 可组合 `FlagOI`、`FlagCI`、`FlagNP`、`FlagIO`。
- `EffectiveACE`：在 `ACE` 外增加 `Source` 与显式列表原始 `Index`。
- `Decision`：返回是否允许、`ResultGrant/ResultDenyHit/ResultImplicitDeny`、决定性条目位置与来源、是否继承、当前已授予位 `Granted`。
- `AddNode(id, parent, container)`、`SetACL(node, aces, protected)`、`Move(node, newParent)` 均为原子操作；成功后 `Version()` 增加 1。

### 显式列表与继承

`SetACL` 保留输入 ACE 数组的内容与长度，但节点参与计算的显式顺序 `X(n)` 固定为：

1. 所有拒绝 ACE，保持相对顺序。
2. 所有允许 ACE，保持相对顺序。

有效列表递归定义为：

- 根：`E(/) = X(/)`。
- 非根且未保护：`E(n) = X(n)` 后接由 `E(parent)` 按父列表原顺序变换出的副本。
- 非根且保护：`E(n) = X(n)`，不读取保护节点以上祖先。

继承变换逐条复制来源节点、允许类别、主体和掩码：

| 子节点类型 | 父 ACE 标志 | 副本标志 |
| --- | --- | --- |
| 对象 | 含 `OI` | `0` |
| 对象 | 不含 `OI` | 不产生 |
| 容器 | 含 `CI`，含 `NP` | `0` |
| 容器 | 含 `CI`，不含 `NP` | `Flags & (OI|CI)` |
| 容器 | 不含 `CI`、含 `OI`，含 `NP` | 不产生 |
| 容器 | 不含 `CI`、含 `OI`，不含 `NP` | `OI|IO` |
| 容器 | 不含 `CI/OI` | 不产生 |

继承部分始终沿用父有效列表自身顺序，不再次全局“拒绝优先”。带 `IO` 的条目保留在 `E(n)` 中且继续向下变换，但不参与节点 `n` 自身的判定。

### Eval 判定

`Eval(token, node, R)` 按 `E(node)` 顺序扫描，初始 `G=0`。主体不在非空 `token` 中或带 `IO` 的条目会被跳过但仍计入处理条目数。

- 拒绝条目：若 `Mask & R & ^G != 0`，立即返回命中拒绝；若只触及已授予位或不在 `R` 中，则忽略，已授予位不会被收回。
- 允许条目：执行 `G |= Mask & R`；若 `G == R`，立即返回授予。
- 扫描结束仍未得到全部 `R`：返回隐式拒绝，决定性下标为 -1，`Granted=G`。

非导出计数器分别记录 Eval 实际处理的条目数与构造有效列表时读取显式列表的节点数。节点计数只包含从目标节点到最近保护节点（含）或根（含）的链；处理条目数在提前授予/拒绝时只计到决定性条目。`Effective` 不增加这两个计数。

### 错误类别

错误依次区分为参数非法、节点不存在、冲突、超限；同一次调用只返回按该顺序遇到的第一个错误。`Move` 先检查 `node`，再检查 `newParent`。深度超限时按移动后整个子树的最大深度判断。任何失败调用都不改变树、ACE、保护开关或版本。

所有读写由同一把读写锁保护，读操作看到某一时刻的一致快照，`SetACL` 与 `Move` 的效果对观察者整体可见。

### 本地验证

测试包含题设示例、标志变换、保护节点、Move、计数口径、失败回滚，以及 2000 组固定随机树/ACL/判定序列与独立朴素递归模拟器对照。随机测试使用 `go test -v` 时逐条打印输入、输出和判定依据。

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

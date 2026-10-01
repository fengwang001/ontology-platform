# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## cgroup v2 风格内存保护层级

实现在 `cgroupmemory` 包中。根分组固定为 `/`；其他路径必须以 `/` 开头、由非空段组成，且不能以 `/` 结尾。只有叶子分组保存直接用量，非叶子分组的用量等于全部叶子后代用量之和。

### 有效保护推导

对非根分组 `c`，先计算其有效 low 候选值：

```text
l_c = min(usage_c, low_c)
```

若父分组是根，则 `E_c = l_c`。若父分组 `p` 不是根，令：

```text
S_p = Σ l_child
```

- `S_p <= E_p` 时：`E_c = l_c`
- 否则：`E_c = floor(E_p · l_c / S_p)`

有效 min 先用同样的层级规则推导原始值：

```text
m_c = min(usage_c, min_c)
Sm_p = Σ m_child
```

父分组是根时 `raw_c = m_c`；否则在 `Sm_p > Em_p` 时按 `floor(Em_p · m_c / Sm_p)` 分配。最后夹取为：

```text
Em_c = min(raw_c, E_c)
```

比例分母使用受用量夹取后的 `l_c`/`m_c`，不是配置额度本身。所有聚合和比例计算使用 `math/big.Int`，可精确处理最高约 `10^30` 的乘积。

### 两遍回收

`Reclaim(need)` 在调用开始时一次性计算全树当前的 `E` 与 `Em`，同一次调用内不随回收进度重算。

1. 第一遍计算每个叶子的 `over1 = usage - E`，仅收集正值；按回收量降序、路径字节序升序处理，每次回收 `min(剩余需求, over1)`。
2. 若第一遍后需求仍大于 0，第二遍基于第一遍结束后的当前用量计算 `over2 = current_usage - Em`，仍只收集正值，并按 `over2` 降序、路径字节序升序回收。
3. 同一叶子最多在每遍各出现一次；总回收量为 `min(need, 两遍可回收量之和)`，无法满足时 `Insufficient=true`，这不是错误。

例如 `/a`（usage 100，min 20，low 60）与 `/b`（usage 100，min 0，low 0）在根下，`Reclaim(150)` 返回：

```text
(/b, 100, pass 1), (/a, 40, pass 1), (/a, 10, pass 2)
```

`Reclaim(250)` 只能回收 180，并返回不足标记。

### 并发与拒绝顺序

所有公开操作由同一把互斥保护，对外等价于某个全序串行执行。被拒绝的操作不会修改树、额度或用量。拒绝时按以下顺序返回第一个原因：

1. 参数非法：路径语法、`min/low/bytes/need` 越界，或 `min > low`
2. 根不可操作：对根执行 `Create`、`Remove`、`SetProtect`、`SetUsage` 或 `Effective`
3. 分组不存在
4. `Create` 的路径已存在
5. `Create` 的父分组不存在
6. `Create` 的父分组是直接用量大于 0 的叶子
7. `SetUsage` 作用于非叶子
8. `Remove` 作用于仍有子分组的分组

### 本地验证

```bash
# 全量测试
GOCACHE=/tmp/go-cache /usr/local/go/bin/go test ./...

# 竞态检测
GOCACHE=/tmp/go-cache /usr/local/go/bin/go test -race ./...

# 随机 2000 组朴素大整数模型对照，并查看每条输入、输出与判定依据
GOCACHE=/tmp/go-cache /usr/local/go/bin/go test ./cgroupmemory \
  -run TestRandomOperationsAgainstNaiveModel -v -count=1

# 格式化和 vet
/usr/local/go/bin/gofmt -w cgroupmemory
GOCACHE=/tmp/go-cache /usr/local/go/bin/go vet ./...
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

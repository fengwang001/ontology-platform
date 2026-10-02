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

## 多数派租约锁

`redlock` 包实现带注入时钟、可确定重放的 Redlock 式租约获取器。构造参数为节点数 `N`、单节点超时 `Tn`、漂移千分数 `D` 与最大租期 `MaxTTL`。

### 节点与客户端时钟

- 每个节点独立保存「资源名 → (令牌, 到期时刻)」，并有静默截止 `q`，初值为 `0`。
- `Acquire(res,ttl,start,rtt)` 使用的客户端时钟 `c` 初值为 `start`，按节点 `0..N-1` 依次处理。
- `rtt[i] = -1` 表示不可达：节点不执行，`c += Tn`。
- 可达请求的到达时刻为 `a = c + floor(rtt[i]/2)`。
- `rtt[i] <= Tn` 时请求结束后 `c += rtt[i]`；`rtt[i] > Tn` 视为超时，节点仍可能已在 `a` 写入，但客户端只推进 `c += Tn`。

### 授予条件与有效时长

节点在到达时刻 `a` 执行 NX 写入：

- `a < q`：拒绝。
- 已存在该资源且记录的到期时刻 `> a`：拒绝；旧记录属于同一客户端或同一令牌也不例外。
- 否则写入 `(k, a+ttl)`，覆盖不存在或已到期的旧记录，并视为本次节点授予。

只有 `rtt[i] <= Tn` 且节点授予的节点计入有效授予数 `g`。处理结束后：

```text
cend = c
dr   = floor(ttl * D / 1000) + 2
v    = ttl - (cend - start) - dr
Until = start + ttl - dr = cend + v
```

当 `g >= floor(N/2)+1` 且 `v > 0` 时获取成功。多数派达到但 `v == 0` 仍失败。

### 失败释放与节点重启

获取失败是正常结果，仍会消耗下一个令牌、推进引擎时钟到 `cend`，并在 `cend` 向全部 `N` 个节点发送释放，包括不可达与超时节点。释放只删除同一资源、同一令牌的记录，不检查记录是否到期。

`Restart(i,now)` 清空节点 `i` 的全部记录，并设置：

```text
q = now + MaxTTL
```

因此到达时刻小于 `q` 一律拒绝，恰等于 `q` 可以授予。`Unlock(res,k,now)` 删除所有同资源、同令牌记录且不看到期；`Count(res,k,now)` 只统计到期时刻严格大于 `now` 的记录。

### 拒绝顺序与并发

操作参数校验顺序固定为：参数非法、时间非法、时钟回退。`Acquire` 依次检查资源、TTL 与 RTT，再检查 `start`，最后检查 `start < T`；`Unlock`、`Restart`、`Count` 也遵循同样三类顺序。被拒绝的操作不改变节点记录、静默截止、引擎时钟或令牌计数。

所有公开操作在实例内串行化，因此并发调用的结果等价于某个合法串行顺序。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测
go test -race ./redlock

# 查看 2000 组随机输入、朴素模拟输出、状态差异与区间不交判定日志
go test -run TestRandomNaiveComparisonAndDisjointIntervals -v ./redlock
```

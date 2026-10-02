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

## RFC 8628 风格设备授权流

核心实现位于 `ontology/device_flow.go`，入口类型为 `ontology.New(Config)`。配置字段包括：

- `E`：设备码有效期；`I0`、`D`、`Imax`：初始轮询间隔、步进和上限。
- `H`：过快事件的惩罚窗口；`Cmax`：单客户端活跃授权上限。
- `Z`：限流阈值；`Gen`：可注入的原始用户码生成函数。
- 任一配置越界、`I0 > Imax` 或 `Gen == nil` 时，构造整体返回参数非法。

### 用户码

用户码规范化规则为删除所有 `-`，小写字母转为大写；规范化结果必须是 4 到 16 个大写字母或数字。`Start` 保存生成器返回的原串，但用规范化结果比较：

- `ABCD-EFGH`、`ABCDEFGH`、`abcdefgh` 等价。
- 不合规生成结果算一次失败尝试。
- 与任一在当前时刻尚未到期的授权（含 `approved`、`denied`、`consumed`）重复也算一次失败。
- 已到期授权的用户码可以复用。
- 连续 100 次失败返回生成失败；成功时 `gen` 调用次数为失败数加一。

### 限流与间隔

某客户端的惩罚次数 `s` 是该客户端名下满足 `t + H > now` 的过快事件数。客户端基础间隔为：

```text
min(Imax, I0 + D * s)
```

新授权不继承旧授权的轮询间隔，而使用发起时刻计算出的客户端基础间隔。`Start` 的拒绝次序为参数非法、时钟回退、限流、超限、生成失败。

限流时把窗口内事件按发生时刻升序排列，若有 `s` 个窗口事件，则：

```text
u = 第 (s - Z + 1) 个窗口事件的时刻 + H
```

例如 `Z=2`、窗口事件为 `[3,12,20]`、`H=100` 时，`s=3`，`u=112`；当事件 `3` 出窗后为 `[12,20]`，`s=2` 仍限流，`u` 仍为 `112`。

### 轮询顺序与状态

被拒绝的操作（参数非法、时钟回退、未知设备码）不会推进全局时钟，也不修改授权、事件或计数器。被接受的 `Poll` 严格按以下顺序返回：

1. `consumed`：无效授权。
2. `now >= expiresAt`：已过期。
3. `now < nextAllowed`：过快。
4. `pending`：等待授权，并把 `nextAllowed` 设为 `now + interval`。
5. `approved`：颁发递增令牌，随后置为 `consumed`。
6. `denied`：拒绝访问，随后置为 `consumed`。

过快判定先于授权状态判定。过快时执行：

```text
interval = min(Imax, interval + D)
nextAllowed = now + interval
```

即使已经处于 `Imax`，仍会记录一条过快事件，并以 `Imax` 重设下一次允许时刻。`now == nextAllowed` 是允许轮询，早 1 秒才是过快。

### 名额占用

活跃授权指状态为 `pending` 或 `approved`，且 `now < expiresAt` 的授权：

- `denied` 在授权决定时立即释放。
- `approved` 只有在被成功轮询并置为 `consumed` 后才释放；若未领取并到期，到期前持续占用。
- `consumed` 和已到期授权不占用名额。

每个客户端使用按到期时刻排序的小顶堆维护活跃名额；过快事件按客户端保序保存并用窗口游标/二分读取统计。两者的摊还复杂度均与该客户端历史授权总数无关。

### 并发与验证

所有操作由服务内互斥锁串行化，因此并发调用等价于某个合法串行顺序：同一设备码并发成功轮询恰有一个返回令牌，同一用户码并发批准/拒绝恰有一个决定成功。

非导出字段 `genCalls` 与 `expiryPops` 可供同包测试检查：前者记录最近一次 `Start` 的生成器调用次数，后者记录发起成功时到期回收的堆弹出次数，弹出次数不超过本次到期授权数加一。

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

当前容器若未将 Go 放入默认 `PATH`，可使用：

```bash
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/tmp/go-cache-ontology
gofmt -w ontology/*.go
go test ./...
go test -race -v ./...
go vet ./...
```

随机朴素模型对照位于 `ontology/device_flow_random_test.go`，默认执行 2000 组随机发起、批准、拒绝与轮询序列；用 `-v` 可查看每个操作的输入、输出和判定依据。

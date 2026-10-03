# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## merger：周期定时器唤醒合并器

`merger` 包实现带最小唤醒间隔 `g` 与单次批量上限 `B` 的唤醒合并器，
把各定时器的容忍窗口合并成尽量少的实际唤醒。

### 唤醒时刻的取法

- 每个定时器的窗口为 `[n, n+s]`（`n` 为下一个名义时刻，`s` 为容忍度）。
- 记 `e` 为全部定时器 `n+s` 的最小值，`last` 为上一次实际唤醒时刻，
  则下一次唤醒时刻 `w = max(e, last+g)`；尚无唤醒时 `w = e`。
- `Next()` 只计算 `w`，不改变任何状态。

### 批量上限与追赶规则

- `Wake()` 在 `w` 时刻触发：候选为全部满足 `n <= w` 的定时器，
  按 `(n+s, 编号字节序)` 升序取前 `B` 个触发，其余留下参与下次唤醒。
- 被触发的定时器按名义栅格追赶：跳过数 `k = floor((w-n)/P)`
  （名义时刻 `n+P … n+kP` 并入本次，不计触发也不计延迟），
  随后 `n` 增加 `(k+1)*P`，保证新 `n` 严格大于本次 `w`。
- `AdvanceTo(t)` 在 `Next() <= t` 时重复 `Wake()`，单次调用至多 1e5 次，
  达上限即停（不报错），时钟停在最后一次唤醒时刻。

### 延迟与跳过的定义

- 延迟 `late = max(0, w-(n+s))`：窗口末端被错过多少。
- 跳过数 `k`：被并入本次唤醒的名义周期数。
- `Stats()` 返回累计唤醒数、触发数、延迟次数（`late > 0` 的触发数）与跳过总数。

### 复杂度

- 以 `(n+s, 编号)` 与 `(n, 编号)` 为键各维护一个索引堆，随触发、`Add`、
  `Remove` 增量更新：`Next()` 取堆顶为 O(1)（0 次键比较），
  增删与键值修复为 O(log N)。
- `Wake()` 只从以 `n` 为键的堆中弹出候选，考察次数 = 候选个数 + 1，
  不做全表扫描。
- 所有操作经互斥锁串行化，可并发调用，结果等价于某个串行顺序。

### 本地验证

```bash
# 全部确定性用例 + 2000 组随机序列与朴素模拟对拍
go test ./merger/

# 查看对拍日志（每组序列打印输入、输出与判定依据）
go test ./merger/ -run TestDifferentialAgainstNaive -v

# 竞态检测
go test -race ./merger/
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

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

## 云承诺折扣用量匹配器

`CommitMatcher` 支持并发调用：`AddCommit` 登记承诺，`Apply(t, lines)` 将处理点推进到小时 `t`，并返回 `lastHour+1` 到 `t` 的逐小时报告。内部用互斥锁保证并发操作等价于某个串行顺序。

### 覆盖顺序

每个小时只取区间 `[start, start+n)` 内有效的承诺，按折扣 `d` 降序排列；`d` 相同时按 `id` 升序。承诺 `fam=0` 可覆盖任意族，其他承诺只能覆盖同族用量行，并始终保持用量行输入顺序。

对承诺设剩余承诺额 `R=h`、折后比例 `m=10000-d`，依次扫描兼容且仍有剩余按需价 `p'` 的行：

- 整行覆盖：当 `R >= e`，其中 `e=ceil(p'*m/10000)`，扣减 `R -= e`，覆盖按需价 `p'`。
- 部分覆盖：当 `0 < R < e`，覆盖 `q=floor(R*10000/m)`，更新 `p' -= q`、`R=0`，该承诺停止扫描。
- 一行被部分覆盖后保留在后续承诺的扫描序列中，可继续被覆盖。
- 每小时报告中 `used=h-R`、`unused=R`、`covered` 为该承诺累计覆盖的按需价。

### 摊销与账单

预付 `up` 摊到 `n` 个有效小时：

- 前 `n-1` 个有效小时各计 `floor(up/n)`。
- 最后一个有效小时计 `up-(n-1)*floor(up/n)`，因此摊销尾项吸收不能整除的部分。
- 有效区间右端 `start+n` 所在小时立即过期，不再收固定承诺费或摊销。
- 小时账单等于所有有效承诺的 `h+当小时摊销`，加上所有用量行最终剩余按需价之和。

### 跳过小时

`Apply(t, lines)` 可一次推进至多 10000 小时。`lastHour+1` 到 `t-1` 视为没有用量行，但仍逐小时计入有效承诺的固定费和摊销；只有第 `t` 小时使用 `lines`。这与逐小时调用得到的小时报告序列完全一致。

### 本地验证

```bash
# 全量测试；随机测试包含 2000 组与朴素逐行模拟器的对照
go test -v ./...

# 并发验证
go test -race ./...

# 若默认 Go 构建缓存不可写，可指定临时缓存
GOCACHE=/tmp/go-cache go test ./...
```

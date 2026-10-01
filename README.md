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

## 瑞士制轮次配对器

实现位于 `swiss` 包，构造函数 `swiss.New(R)` 只接受 `1 <= R <= 20`。

- `Register(name)`：名称必须非空且互不相同；第一次成功 `Pair()` 后登记关闭，种子号从 1 开始按登记顺序分配。
- `Pair()`：开始新一轮。选手数为奇数时，先在从未轮空者中选择积分最低者；积分相同选种子号最大者。其余选手按“积分降序、种子号升序”排列。
- 配对规则：反复取序列中第一个未配对选手 `x`，在其后选择第一个未配对且从未与 `x` 交手的选手 `y`；不交换、不回溯。若 `x` 找不到对手，整轮失败且不记录轮空、不推进轮次。
- 轮空口径：轮空立即得 2 分，但不产生对手、不计入任何选手的对手分；每名选手至多轮空一次，任意两名选手至多交手一次。
- `Report(a, b, r)`：从种子号 `a` 的角度登记，`r` 为 2（胜）、1（平）、0（负），`b` 得 `2-r`；反向传种子时得分方向也反向。当前轮所有盘登记后自动封轮。
- `Standings()`：返回种子号、积分和对手分，按“积分降序、对手分降序、种子号升序”排序。对手分为所有真实对手的当前积分之和，并在每次查询时按当前积分重新计算。
- 并发性：所有会读取或改变状态的公开方法使用同一把互斥锁，保证并发调用等价于某种合法的串行交错；被拒绝的操作不改变状态。

### 本地验证

```bash
# 全量测试与竞态检测
go test ./...
go test -race ./...

# 查看 2000 组随机比赛的输入、输出与判定依据日志
go test -run TestRandomTournamentsMatchNaive -count=1 -v ./swiss

# 格式化与静态检查
gofmt -w swiss
go vet ./...
```

随机测试使用固定随机种子，将正式实现与测试内按规则逐步重放的朴素状态机逐操作对照，因此相同命令可复现相同的对阵、轮空、报分和榜单。

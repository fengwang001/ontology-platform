# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 增强二次机会时钟置换器

`ClockReplacer` 位于根包，使用 `New(n)` 创建 `n` 个帧，扫描指针初始为 0：

- `Access(page, write)`：命中时引用位置 1，写访问同时置脏位。
- 缺页且有空帧：装入编号最小的空帧，引用位置 1，脏位由 `write` 决定，指针不动。
- 缺页且无空帧：从指针位置开始，每趟顺时针恰好扫描一圈。
- 甲趟：选择第一个 `(引用位=0, 脏位=0)` 的帧，不修改任何位。
- 乙趟：选择第一个 `(引用位=0, 脏位=1)` 的帧；选中前扫过的引用位为 1 的帧全部清零。
- 甲、乙两趟交替执行直到选中牺牲页；若牺牲页为脏，写回计数加一。
- 新页装入牺牲帧后置引用位为 1、脏位为是否写，指针移到下一帧；末帧之后回绕到 0。
- `Flush(page)`：只允许刷写驻留且脏的页，写回计数加一、清脏位，引用位保持不变。

错误均为哨兵错误，可用 `errors.Is` 区分：

- `ErrInvalidFrameCount`：帧数小于 1。
- `ErrNegativePage`：页号为负，访问和刷写都优先返回该错误。
- `ErrPageNotResident`：刷写页不驻留。
- `ErrPageNotDirty`：刷写页驻留但不脏。

所有访问、刷写和快照查询由同一把互斥锁保护，结果等价于某一种串行交错。被拒绝的操作不会修改帧、指针或写回计数。

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 本置换器的详细测试（包含输入、输出、判定依据和朴素模拟日志）
go test -v .
go test -v -run 'TestFourBitCombinations|TestRandomOperations' .

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

若 Go 缓存目录在当前环境只读，可为本地命令指定临时缓存：

```bash
GOCACHE=/tmp/go-cache go test -race -v ./...
```

测试内的独立朴素模拟器逐条实现相同规则，随机访问/刷写序列会同时校验：

- 四种引用位、脏位组合的甲趟/乙趟选择顺序。
- 乙趟清位后，下一轮甲趟能够立即选中。
- 所有引用位均为 1 时必须先经过甲、乙两趟再选中。
- 末帧驱逐后的指针回绕、空帧装入不移动指针、刷写不清引用位。
- 驱逐和成功刷写共同贡献的写回总数。
- 相同序列重放以及并发执行后的帧容量、驻留页唯一性和状态一致性。

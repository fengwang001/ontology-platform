# fsmapper：跨平台文件名安全映射器

把源端目录树中的文件名映射为目标端（不区分大小写、禁用部分字符与保留名、
限制名字与路径字节数）可用的名字；同一目录内保持名字唯一，且已分配名字
只在条目删除或改名时释放，不因后续增删而漂移。相同调用序列重放得到完全
相同的映射。

## 构造参数

`New(maxBytes, maxPath, maxEntries)`：

- `MaxBytes`：单个映射名字节上限，范围 16..255。
- `MaxPath`：映射后整条路径字节上限，范围 `MaxBytes`..4096。
- `MaxEntries`：每个目录条目数上限，范围 1..100000。

根目录 id 为 0；条目的路径长度 = 全部祖先与自身映射名字节数之和，加上
各段之间的 `/` 个数（根下条目即为映射名长度）。

源名 `src` 必须非空、不含 `/` 与 NUL、且为合法 UTF-8，否则 `ErrInvalidName`。

## 四步基础映射

1. **转义**：逐个 rune，属于 `< > : " \ | ? *`、码点小于 0x20、或 `%`
   的字节替换为 `%` 加两位大写十六进制；其余原样保留（多字节 rune 不拆分）。
2. **结尾点/空格**：结果以 `.` 或空格结尾时，把最后一个字符替换为
   `%2E` 或 `%20`。
3. **保留名**：取第一个 `.` 之前的部分为主干，主干按 ASCII 大写折叠
   （仅 A-Z→a-z）后属于 `CON PRN AUX NUL COM1..9 LPT1..9` 时，把整个
   名字的第一个字符替换为 `%XX`（如 `Aux.txt` → `%41ux.txt`）。
4. **超长截断**：结果字节数大于 `MaxBytes` 时，令 `h` 为第三步结果全部
   字节的 32 位 FNV-1a（基数 2166136261，乘数 16777619）的 8 位大写
   十六进制；名字按记号（一个 rune，或一个 `%XX`）切分，取最长记号
   前缀 P 使 `len(P)+9 <= MaxBytes`，结果为 `P + "~" + h`，绝不切断
   `%XX` 或多字节 rune。

例：`a:b?.` → `a%3Ab%3F%2E`；`CON` → `%43ON`；`NUL.` → `NUL%2E`
（第二步先转义结尾点，第三步看到的主干是 `NUL%2E`，不再是保留名）。

## 目录内唯一性与稳定性

- 以映射名 ASCII 折叠后的字符串为键。
- `Add`：基础名键未被占用即分配基础名；否则对 `n=2,3,…` 尝试
  `base + "~" + n + ext`，其中以最后一个下标 >0 的 `.` 拆分基础名为
  `base/ext`。编号前会剥掉 `base` 末尾已有的一个 `~<数字>`，使
  `readme~2.MD` 在 `~2`、`~3` 键均被占时直接得到 `readme~4.MD`。
- 候选超过 `MaxBytes` 时从 `base` 末尾按记号缩短到刚好放下；
  `base` 缩不下去（连最短记号都放不下）报 `ErrCannotFit`。
- 已分配名字只在条目删除或改名时释放，其它条目的名字不变。

例：依次添加 `Readme.md`、`README.md`、`readme.MD`、`readme~2.MD`
得到 `Readme.md`、`README~2.md`、`readme~3.MD`、`readme~4.MD`；
删除 `Readme.md` 后再添加 `ReadMe.md` 得到 `ReadMe.md`。

## 改名语义

`Rename(parent, src, newSrc)` 文件与目录均可，条目 id 不变：

- 先释放 `src` 的折叠键，再按 `Add` 的规则为 `newSrc` 分配
  （保证“先释放后分配”：仅有 `Readme.md` 时改名为 `readme.md`
  得到 `readme.md`，而不是 `readme~2.md`）。
- `newSrc == src` 为成功的空操作。
- 任何失败（`ErrExists`/`ErrCannotFit`/`ErrPathTooLong`）都恢复为
  调用前状态。

## 路径长度判定

- `Add`：新条目映射路径长度超过 `MaxPath` 报 `ErrPathTooLong` 并回滚。
- `Rename`：对受影响整棵子树施加“新映射名长度 − 旧映射名长度”增量，
  任一节点超过 `MaxPath` 即拒绝并回滚（目录与全部后代路径均不变）。
  例：`MaxBytes=16, MaxPath=24`，`projects/readme.txt`（19）目录改名为
  `projects-archive`（16）后子树最长 27 > 24，被拒。

## 错误优先级

只报固定优先级中的第一个错误，被拒调用不改任何状态。

- `Add`：`ErrInvalidName` > `ErrNoParent` > `ErrExists`（源名大小写
  敏感）> `ErrFull` > `ErrCannotFit` > `ErrPathTooLong`。
- `Rename`：`newSrc` 的 `ErrInvalidName` > `src` 的 `ErrNotFound` >
  `newSrc==src` 空操作 > `ErrExists` > `ErrCannotFit` >
  `ErrPathTooLong`（改名不报 `ErrFull`）。
- `Remove`：非法名 > `ErrNoParent` > `ErrNotFound` > `ErrNotEmpty`。

只读查询：`Lookup(parent, src)`（源名大小写敏感）、`Path(id)`
（根返回空串）、`Names(parent)`（映射名按字节序）。

## 并发

所有方法可并发调用，通过单一互斥锁保证语义等价于某个串行顺序；
任意时刻同一目录内折叠键两两不同、名字不超过 `MaxBytes`、
路径不超过 `MaxPath`，且映射结果中不含目标端禁用字符。

## 本地验证

```bash
# 全部测试（含 2000 组随机树操作与朴素实现对照）
go test ./fsmapper/ -v

# 竞态检测（含并发增删改查）
go test -race ./fsmapper/

# 仅看 2000 组对照日志（输入、输出、判定依据）
go test ./fsmapper/ -run TestRandomDifferential -v

go vet ./...
gofmt -l fsmapper/
```

测试结构：

- `fsmapper_test.go` / `behavior_test.go`：三个基础映射例子、转义
  单射性、保留名（带扩展名/前导点主干）、记号边界截断、候选缩短与
  `ErrCannotFit`、跳号、删除复用与不漂移、改名先释放后分配、目录
  改名子树路径判定与回滚、错误优先级。
- `naive_test.go`：严格按规则逐步重写、与生产代码不共享映射函数的
  朴素参考实现。
- `diff_test.go`：2000 个独立随机种子的树操作序列对照，每步后比较
  全量快照（id/源名/映射路径/isDir/路径长度），并打印输入、输出
  与判定依据。
- `concurrent_test.go`：8 worker 并发增删改查，配合 `-race` 检测。

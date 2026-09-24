# CDC schema 演进列映射 — 推导与不变量

## 六版演进
v1 `[x:int,y:str,m:str]` → v2 `+z:str(required)` → v3 `y→int` → v4 `x→str` → v5 `-m` → v6 `+w:int(optional)`；active=v6=`[x:str,y:int,z:str,w:int]`。

## 六行映射表（逐列按 active 序 x,y,z,w）
| # | 事件(版本,值) | 逐列推导 | 结果 |
|---|---|---|---|
| 1 | (v6,["1",2,"a",3]) | 同版四列直取 | `["1",2,"a",3]` |
| 2 | (v2,[5,"6","M","b"]) | x:5→"5"；y:"6"→6；z="b"；w 缺、optional→0；m 丢弃 | `["5",6,"b",0]` |
| 3 | (v2,[5,"abc","M","b"]) | y:"abc"→int 解析失败 | `ErrBadValue` 整事件拒收 |
| 4 | (v4,["9",10,"M","d"]) | x="9"；y=10；z="d"；w 缺、optional→0 | `["9",10,"d",0]` |
| 5 | (v1,[11,"12","M"]) | z 缺且 required=true | `ErrMissingColumn` 拒收 |
| 6 | (v3,[13,14,"M","e"]) | x:13→"13"；y=14；z="e"；w 缺→0 | `["13",14,"e",0]` |

**(甲)** 事件4=`["9",10,"d",0]`，事件5=`ErrMissingColumn`。若缺列一律补零：事件5错成 `["11",12,"",0]`（z 被静默补 ""）；若一律拒收：事件4错成 `ErrMissingColumn`（w 本可补 0）。
**(乙)** 事件2=`["5",6,"b",0]`，事件3=`ErrBadValue`。若 atoi 忽略错误：事件3错成 `["5",0,"b",0]`（y 静默为 0）；若 str→int 一律拒：事件2错成 `ErrBadValue`（y 本可转 6）。
**(丙)** 按位置对齐事件2（v2 序 `[x,y,m,z]`）：z 错成 `"M"`（拿到 m 列的值），w 错成 `"b"` 且 `"b"→int` 非法 ⇒ 整事件 `ErrBadValue`（若 w 是 str 则错成 `"b"`）。x、y 同时改型仍可判定：coercion 逐列独立，各按「该列在事件版的旧类型→active 新类型」查表——x 是 int→str 恒成功，y 是 str→int 由 ParseInt 判定，互不干扰。

## 四条不变量及其落点
1. **与朴素重算一致**：`mapc.Map` 逐 active 列按名查事件布局+coercion+补零/拒收；由 `TestAgainstNaive`（随机版本/事件对照朴素参照）钉住。
2. **列名对齐**：`mapc.Map` 用 name→index 哈希定位，绝不按下标；由 `TestNameAlignment`（删列/后加列不错位）钉住。
3. **可判定**：coercion 表总终止且每列结果唯一，错误为互异哨兵；由 `TestDeterministic`（同事例反复 Map 结果相同）钉住。
4. **失败不留痕**：`sch.Registry` 所有写操作先整体校验再提交，校验失败零写入；由 `TestRejectNoSideEffect` 钉住。

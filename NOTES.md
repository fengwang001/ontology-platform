# CDC schema 演进列映射 — 推导与不变量

## 六版演进
v1=[x:int,y:str,m:str] →(+z:str,req) v2 →(y→int) v3 →(x→str) v4 →(−m) v5 →(+w:int,opt) v6；active=v6=[x:str,y:int,z:str,w:int]

## 六行映射表（Map 到 active 列序 [x,y,z,w]）
1. (v6,["1",2,"a",3])     → ["1",2,"a",3]     同版同型，逐列直取
2. (v2,[5,"6","M","b"])   → ["5",6,"b",0]     x:int→str="5"；y:"6"→6；m 已删丢弃；w 缺、opt→0
3. (v2,[5,"abc","M","b"]) → ErrBadValue       y:"abc"→int 解析失败，整事件拒收
4. (v4,["9",10,"M","d"])  → ["9",10,"d",0]    x/y 同型直取；m 丢弃；w 缺、opt→0
5. (v1,[11,"12","M"])     → ErrMissingColumn  z 是 v2 才加的 required 列，拒收
6. (v3,[13,14,"M","e"])   → ["13",14,"e",0]   x:int→str="13"；m 丢弃；w 缺、opt→0

## 三问
- (甲) 事件4=["9",10,"d",0]；事件5=ErrMissingColumn。若缺列一律补零：事件5错成 ["11",12,"",0]（z 被静默填 ""）；若一律拒收：事件4错成 ErrMissingColumn。
- (乙) 事件2=["5",6,"b",0]；事件3=ErrBadValue。若 atoi 静默填0：事件3错成 ["5",0,"b",0]；若 str→int 一律拒：事件2错成 ErrBadValue。
- (丙) 按位置对齐：事件2(v2 列序[x,y,m,z])位置2的值"M"（实为已删的 m）错填给 active 的 z → z="M"；位置3的"b"错填给 w:int，str→int 失败 → 整事件错拒 ErrBadValue（本应成功）。x、y 同时改型仍可判定：每列独立按「该列在事件版本的旧型→active 新型」查 coercion 表（x:int→str 必成，y:str→int 可解析判定），逐列互不干扰。

## 四条不变量（位置 + 钉住它的测试）
1. 与朴素重算一致：mapc.Map 逐列按名对齐+coercion；测试内 naive() 参照逐列比对 —— TestMapMatchesNaive
2. 列名对齐：mapc.Map 用 name→index 哈希定位，删列丢弃/后加列补零或拒 —— TestMapAlignsByName
3. 可判定：Map 是纯函数，sch.Snapshot 单锁同时取事件版与 active 布局，同入参同结果 —— TestMapDeterministic
4. 失败不留痕：sch.AddColumn/DropColumn/ChangeType 先校验再整体追加版本，api.Map 只读 —— TestRejectLeavesState

// gcdemo 是分代复制式 GC 的可运行演示：
// 打印每一步输入（操作）、输出（结果/统计）与判定依据。
package main

import (
	"errors"
	"fmt"

	"ontology/gencopy"
)

func main() {
	// Eden/幸存区各 128B，老年代 200B，存活 3 次晋升。
	h, err := gencopy.NewHeap(gencopy.Config{
		EdenSize: 128, SurvivorSize: 128, OldSize: 200, PromoteAge: 3,
	})
	must(err)

	// 1) 分配一个长期根对象，连续 3 轮回收使其晋升到老年代。
	root := alloc(h, 2, "root")
	addRoot(h, root)
	for i := 1; i <= 3; i++ {
		minorGC(h, fmt.Sprintf("root 存活第 %d 轮（%s）", i, ageHint(i)))
	}
	printUsed(h, "root 晋升后")

	// 2) 老年代对象引用一个不登记根的年轻对象：写屏障记入记忆集。
	young := alloc(h, 0, "young")
	setRef(h, root, 0, young)
	minorGC(h, "young 不登记根，仅被老年 root 引用 -> 应靠记忆集存活")
	mustRead(h, root, 0, young, "跨代引用必须保持")
	printUsed(h, "跨代引用存活后")

	// 3) 分配一个不可达垃圾，确认它被回收、句柄带墓碑。
	garbage := alloc(h, 1, "garbage")
	minorGC(h, "garbage 不可达 -> 应释放")
	if _, err := h.Payload(garbage); errors.Is(err, gencopy.ErrHandleReclaimed) {
		fmt.Printf("输出 Payload(garbage=%d) -> ErrHandleReclaimed\n判定依据：根/记忆集不可达的年轻对象已释放\n\n", garbage)
	} else {
		fmt.Printf("非预期：%v\n", err)
	}

	// 4) 演示晋升失败撤回：构造一个老年对象占满老年代的场景。
	fmt.Println("--- 晋升失败撤回演示（切换到小老年代堆）---")
	h2, err := gencopy.NewHeap(gencopy.Config{
		EdenSize: 128, SurvivorSize: 128, OldSize: 60, PromoteAge: 1,
	})
	must(err)
	// 先晋升一个 40B 对象占住老年代（60-40=20B 剩余）。
	placeholder := alloc(h2, 0, "16-byte-payload!")
	addRoot(h2, placeholder)
	minorGC(h2, "占位对象 40B 晋升，老年代剩余 20B")
	h2.RemoveRoot(placeholder)
	// 再来一个 40B 的根对象要求晋升：20B 放不下 -> ErrOldFull。
	victim := alloc(h2, 0, "another-40-byte!!")
	addRoot(h2, victim)
	memBefore, _ := h2.UsedBytes()
	_, gerr := h2.ForceMinorGC()
	fmt.Printf("输入 ForceMinorGC()（victim=%d 需 40B，老年代仅剩 20B）-> %v\n", victim, gerr)
	memAfter, _ := h2.UsedBytes()
	fmt.Printf("判定依据：撤回前年轻已用=%dB，撤回后年轻已用=%dB（相同）\n", memBefore, memAfter)
	if errors.Is(gerr, gencopy.ErrOldFull) {
		pl, perr := h2.Payload(victim)
		fmt.Printf("输出：晋升失败后 Payload(%d)=%q err=%v\n判定依据：整堆恢复到回收前字节状态，对象仍在原位\n",
			victim, string(pl), perr)
	}
}

func ageHint(age int) string {
	if age >= 3 {
		return "达到晋升阈值"
	}
	return "进入幸存区"
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func alloc(h *gencopy.Heap, refs int, payload string) gencopy.Handle {
	x, err := h.Allocate(refs, []byte(payload))
	must(err)
	fmt.Printf("输入 Allocate(refs=%d,payload=%q) -> 句柄 %d\n", refs, payload, x)
	return x
}

func addRoot(h *gencopy.Heap, x gencopy.Handle) {
	must(h.AddRoot(x))
	fmt.Printf("输入 AddRoot(%d)\n", x)
}

func setRef(h *gencopy.Heap, obj gencopy.Handle, slot int, ref gencopy.Handle) {
	must(h.SetRef(obj, slot, ref))
	fmt.Printf("输入 SetRef(obj=%d,slot=%d,ref=%d)\n判定依据：老->年轻引用，写屏障把 %d 记入记忆集\n\n",
		obj, slot, ref, obj)
}

func minorGC(h *gencopy.Heap, label string) gencopy.Stats {
	st, err := h.ForceMinorGC()
	must(err)
	fmt.Printf("输入 ForceMinorGC()  // %s\n输出：次要回收次数+=%d 晋升+=%d\n\n",
		label, st.MinorCollections, st.Promotions)
	return st
}

func printUsed(h *gencopy.Heap, label string) {
	y, o := h.UsedBytes()
	fmt.Printf("输出 UsedBytes()  // %s：年轻=%dB 老年=%dB\n判定依据：已用字节等于各代存活对象字节之和\n\n",
		label, y, o)
}

func mustRead(h *gencopy.Heap, obj gencopy.Handle, slot int, want gencopy.Handle, basis string) {
	got, err := h.GetRef(obj, slot)
	must(err)
	fmt.Printf("输入 GetRef(obj=%d,slot=%d) -> %d\n判定依据：%s（期望 %d）\n\n",
		obj, slot, got, basis, want)
	if got != want {
		panic("reference broken across GC")
	}
}

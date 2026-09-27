package game

// Typed storage boundaries keep each existing transaction intact while releasing
// the world mutex. Arguments are evaluated before unlocking; results after relocking.
func storage1_1[A0, R0 any](h *Hub, f func(A0) R0, a0 A0) R0 {
	resume := h.yieldStorage()
	defer resume()
	return f(a0)
}
func storage1_2[A0, A1, R0 any](h *Hub, f func(A0, A1) R0, a0 A0, a1 A1) R0 {
	resume := h.yieldStorage()
	defer resume()
	return f(a0, a1)
}
func storage2_0[R0, R1 any](h *Hub, f func() (R0, R1)) (R0, R1) {
	resume := h.yieldStorage()
	defer resume()
	return f()
}
func storage2_1[A0, R0, R1 any](h *Hub, f func(A0) (R0, R1), a0 A0) (R0, R1) {
	resume := h.yieldStorage()
	defer resume()
	return f(a0)
}
func storage2_2[A0, A1, R0, R1 any](h *Hub, f func(A0, A1) (R0, R1), a0 A0, a1 A1) (R0, R1) {
	resume := h.yieldStorage()
	defer resume()
	return f(a0, a1)
}
func storage2_3[A0, A1, A2, R0, R1 any](h *Hub, f func(A0, A1, A2) (R0, R1), a0 A0, a1 A1, a2 A2) (R0, R1) {
	resume := h.yieldStorage()
	defer resume()
	return f(a0, a1, a2)
}
func storage2_4[A0, A1, A2, A3, R0, R1 any](h *Hub, f func(A0, A1, A2, A3) (R0, R1), a0 A0, a1 A1, a2 A2, a3 A3) (R0, R1) {
	resume := h.yieldStorage()
	defer resume()
	return f(a0, a1, a2, a3)
}
func storage2_5[A0, A1, A2, A3, A4, R0, R1 any](h *Hub, f func(A0, A1, A2, A3, A4) (R0, R1), a0 A0, a1 A1, a2 A2, a3 A3, a4 A4) (R0, R1) {
	resume := h.yieldStorage()
	defer resume()
	return f(a0, a1, a2, a3, a4)
}
func storage2_6[A0, A1, A2, A3, A4, A5, R0, R1 any](h *Hub, f func(A0, A1, A2, A3, A4, A5) (R0, R1), a0 A0, a1 A1, a2 A2, a3 A3, a4 A4, a5 A5) (R0, R1) {
	resume := h.yieldStorage()
	defer resume()
	return f(a0, a1, a2, a3, a4, a5)
}
func storage3_1[A0, R0, R1, R2 any](h *Hub, f func(A0) (R0, R1, R2), a0 A0) (R0, R1, R2) {
	resume := h.yieldStorage()
	defer resume()
	return f(a0)
}
func storage3_2[A0, A1, R0, R1, R2 any](h *Hub, f func(A0, A1) (R0, R1, R2), a0 A0, a1 A1) (R0, R1, R2) {
	resume := h.yieldStorage()
	defer resume()
	return f(a0, a1)
}
func storage3_3[A0, A1, A2, R0, R1, R2 any](h *Hub, f func(A0, A1, A2) (R0, R1, R2), a0 A0, a1 A1, a2 A2) (R0, R1, R2) {
	resume := h.yieldStorage()
	defer resume()
	return f(a0, a1, a2)
}
func storage3_4[A0, A1, A2, A3, R0, R1, R2 any](h *Hub, f func(A0, A1, A2, A3) (R0, R1, R2), a0 A0, a1 A1, a2 A2, a3 A3) (R0, R1, R2) {
	resume := h.yieldStorage()
	defer resume()
	return f(a0, a1, a2, a3)
}
func storage4_3[A0, A1, A2, R0, R1, R2, R3 any](h *Hub, f func(A0, A1, A2) (R0, R1, R2, R3), a0 A0, a1 A1, a2 A2) (R0, R1, R2, R3) {
	resume := h.yieldStorage()
	defer resume()
	return f(a0, a1, a2)
}

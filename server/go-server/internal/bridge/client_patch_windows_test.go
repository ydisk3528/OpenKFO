package bridge

import (
	"bytes"
	"syscall"
	"testing"
	"unsafe"
)

func TestClientMemoryPatchRequiresExactOriginal(t *testing.T) {
	cases := []struct {
		name            string
		original, fixed []byte
	}{
		{"loading", loadingThreadCleanupOriginal, loadingThreadCleanupSafe},
		{"duration", itemDurationDisplayOriginal, itemDurationDisplayFixed},
		{"shop ornaments", shopOrnamentsFilterOriginal, shopOrnamentsFilterAll},
	}
	for _, patch := range gpkCompatibilityPatches {
		cases = append(cases, struct {
			name            string
			original, fixed []byte
		}{patch.name, patch.original, patch.replacement})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			address, _, err := kernel.NewProc("VirtualAlloc").Call(0, 4096, 0x3000, 0x04)
			if address == 0 {
				t.Fatal(err)
			}
			defer kernel.NewProc("VirtualFree").Call(address, 0, 0x8000)
			buf := unsafe.Slice((*byte)(unsafe.Pointer(address)), len(tc.original))
			copy(buf, tc.original)
			process, err := syscall.GetCurrentProcess()
			if err != nil {
				t.Fatal(err)
			}
			wrong := append([]byte(nil), tc.original...)
			wrong[0] ^= 1
			if patchClientMemory(process, address, wrong, tc.fixed) == nil || !bytes.Equal(buf, tc.original) {
				t.Fatal("mismatched client must not be patched")
			}
			if err := patchClientMemory(process, address, tc.original, tc.fixed); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf, tc.fixed) {
				t.Fatal("patch not installed")
			}
		})
	}
}

package bridge

import (
	"bytes"
	"fmt"
	"syscall"
	"unsafe"
)

// Verified gfld.dat: the worker sets 17C87DC=1 immediately before returning
// (A1661F). Killing it at 98DE6A can interrupt DLL thread-detach cleanup and
// strand a CRT lock. Close only its handle, allowing normal thread teardown.
// Keep the same 15-byte footprint and stdcall stack balance. Disk is untouched.
const loadingThreadCleanupAddress = uintptr(0x98de6a)

var loadingThreadCleanupOriginal = []byte{0x6a, 0x00, 0x8b, 0x15, 0x30, 0x87, 0x7c, 0x01, 0x52, 0xff, 0x15, 0x54, 0x30, 0xb3, 0x00}
var loadingThreadCleanupSafe = []byte{0x8b, 0x15, 0x30, 0x87, 0x7c, 0x01, 0x52, 0xff, 0x15, 0x88, 0x30, 0xb3, 0x00, 0x90, 0x90}

// A1EC20 formats inventory duration/quantity for warehouse and equipment UI.
// Skip only the quota-first branch. The following duration==0 check still
// selects the original quantity path for quantity-only items. Mixed records
// (pet duration + durability) now display time without hiding the list entry.
// Inventory bytes and combat durability consumers are never changed.
const itemDurationDisplayAddress = uintptr(0xa1ec5f)

// Shop::magicWeapon (850B90) sets a subtype=1 filter before querying 10/30.
// Filter 846BD0 rejects subtype=0 head/back ornaments already in that response.
// Zero means all subtypes. Keep the separate subtype=2 pet tab unchanged.
const shopOrnamentsFilterAddress = uintptr(0x850bc9)

var shopOrnamentsFilterOriginal = []byte{0xc7, 0x80, 0x38, 0x07, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}
var shopOrnamentsFilterAll = []byte{0xc7, 0x80, 0x38, 0x07, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

var itemDurationDisplayOriginal = []byte{0x0f, 0x85, 0x20, 0x01, 0x00, 0x00}
var itemDurationDisplayFixed = []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90}

// Verified against the pinned gfld.dat image. The factory result is checked by
// startup; all four subsequent object consumers must be skipped together.
// Addresses are image VAs, not the gfxz.dat disk offsets in login/ClientPatcher.cs.
var gpkCompatibilityPatches = []struct {
	name                  string
	address               uintptr
	original, replacement []byte
}{
	// Skip both argument pushes as well as the stdcall; do not leave eight
	// bytes on the stack as a call-only replacement would.
	{"factory", 0xa3dc77, []byte{0x68, 0xcc, 0xb4, 0xb9, 0x00, 0x8d, 0x85, 0xdc, 0xfd, 0xff, 0xff, 0x50, 0xe8, 0x84, 0xdd, 0xba, 0xff}, []byte{0xb8, 1, 0, 0, 0, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90}},
	{"observer", 0x8224f0, []byte{0x74, 0x77}, []byte{0xeb, 0x77}},
	{"send", 0xa3c994, []byte{0x74, 0x1c}, []byte{0xeb, 0x1c}},
	{"release", 0xa407e5, []byte{0x74, 0x1d}, []byte{0xeb, 0x1d}},
	{"receive", 0xa411dc, []byte{0x74, 0x24}, []byte{0xeb, 0x24}},
}

func patchGPKCompatibility(process syscall.Handle) error {
	for _, patch := range gpkCompatibilityPatches {
		if err := patchClientMemory(process, patch.address, patch.original, patch.replacement); err != nil {
			return fmt.Errorf("GPK compatibility %s: %w", patch.name, err)
		}
	}
	return nil
}

func patchClientMemory(process syscall.Handle, address uintptr, expected, replacement []byte) error {
	if len(expected) == 0 || len(expected) != len(replacement) {
		return fmt.Errorf("invalid client patch length")
	}
	n := uintptr(len(expected))
	current := make([]byte, n)
	var count uintptr
	ok, _, err := kernel.NewProc("ReadProcessMemory").Call(uintptr(process), address, uintptr(unsafe.Pointer(&current[0])), n, uintptr(unsafe.Pointer(&count)))
	if ok == 0 || count != n || !bytes.Equal(current, expected) {
		return fmt.Errorf("unsupported client bytes at %#x: %v", address, err)
	}
	var old uint32
	ok, _, err = kernel.NewProc("VirtualProtectEx").Call(uintptr(process), address, n, 0x40, uintptr(unsafe.Pointer(&old)))
	if ok == 0 {
		return err
	}
	ok, _, writeErr := kernel.NewProc("WriteProcessMemory").Call(uintptr(process), address, uintptr(unsafe.Pointer(&replacement[0])), n, uintptr(unsafe.Pointer(&count)))
	var ignored uint32
	restored, _, restoreErr := kernel.NewProc("VirtualProtectEx").Call(uintptr(process), address, n, uintptr(old), uintptr(unsafe.Pointer(&ignored)))
	if ok == 0 || count != n {
		return fmt.Errorf("client patch write at %#x: %v", address, writeErr)
	}
	if restored == 0 {
		return restoreErr
	}
	ok, _, err = kernel.NewProc("FlushInstructionCache").Call(uintptr(process), address, n)
	if ok == 0 {
		return err
	}
	return nil
}

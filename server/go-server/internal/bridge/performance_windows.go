package bridge

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"
)

type PerformanceOptions struct {
	UnlimitedFrameRate bool `json:"unlimited_frame_rate"`
	HighFrameRate      bool `json:"high_frame_rate"`
	ShowFPS            bool `json:"show_fps"`
}

// Pinned gfld.dat 98c43be7... only. 7EA169 multiplies RenderIntervel by
// milliseconds; neither QPC nor the elapsed-seconds argument is changed.
const frameIntervalAddress = uintptr(0x7ea169)
const presentReturnAddress = uintptr(0x5c7cfa)
const performancePageSize = uintptr(4096)

var presentReturnOriginal = []byte{0x8b, 0xd8, 0x81, 0xfb, 0x68, 0x08, 0x76, 0x88}
var timerStartOriginal = []byte{0x8b, 0x4d, 0x08, 0x89, 0x0d, 0x3c, 0xad, 0x7b, 0x01}
var timerEndOriginal = []byte{0x83, 0x3d, 0xec, 0x86, 0x7c, 0x01, 0x00}

const timerStartAddress = uintptr(0x7ea375)
const timerEndAddress = uintptr(0x7ea3dd)

func timerPeriodCode(code, entry, importSlot uintptr, original []byte) []byte {
	// PUSHFD/PUSHAD; timeBeginPeriod(1) or timeEndPeriod(1); restore state.
	b := []byte{0x9c, 0x60, 0x6a, 0x01, 0xff, 0x15, 0, 0, 0, 0, 0x61, 0x9d}
	binary.LittleEndian.PutUint32(b[6:], uint32(importSlot))
	b = append(b, original...)
	return append(b, relativeJump(code+uintptr(len(b)), entry+uintptr(len(original)))...)
}

func relativeJump(from, to uintptr) []byte {
	b := []byte{0xe9, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(b[1:], uint32(to-from-5))
	return b
}

func presentCounterCode(code, counter uintptr) []byte {
	// Both IDirect3DDevice9::Present (+44) and swap-chain Present (+0C)
	// converge here. Count only S_OK (EAX=0); preserve EAX and reproduce
	// MOV EBX,EAX / CMP EBX,D3DERR_DEVICELOST including flags and stack.
	b := []byte{0x85, 0xc0, 0x75, 0x07, 0xf0, 0xff, 0x05, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(b[7:], uint32(counter))
	b = append(b, presentReturnOriginal...)
	return append(b, relativeJump(code+uintptr(len(b)), presentReturnAddress+uintptr(len(presentReturnOriginal)))...)
}

// Only called while our freshly created client's primary thread is suspended.
// Allocations live until process exit. Data stays RW; code becomes RX.
func patchPerformance(process syscall.Handle, options PerformanceOptions) (uintptr, error) {
	if options.HighFrameRate && options.UnlimitedFrameRate {
		return 0, fmt.Errorf("不能同时启用两种高帧率模式")
	}
	if options.UnlimitedFrameRate {
		for _, p := range unlimitedFramePatches {
			if err := patchClientMemory(process, p.address, p.original, p.replacement); err != nil {
				return 0, fmt.Errorf("高帧率模式 2 不支持当前客户端，请切换普通模式: %w", err)
			}
		}
	}
	if options.HighFrameRate {
		if err := patchClientMemory(process, frameIntervalAddress, []byte{0x6b, 0xc0, 0x0f}, []byte{0x6b, 0xc0, 0x08}); err != nil {
			return 0, err
		}
	}
	if !options.ShowFPS && !options.HighFrameRate {
		return 0, nil
	}
	address, _, err := kernel.NewProc("VirtualAllocEx").Call(uintptr(process), 0, 2*performancePageSize, 0x3000, 0x04)
	if address == 0 {
		return 0, fmt.Errorf("allocate FPS counter: %w", err)
	}
	if address+2*performancePageSize > 1<<32 {
		return 0, fmt.Errorf("FPS counter outside 32-bit address space")
	}
	counter := address + performancePageSize
	code := make([]byte, 256)
	copy(code, presentCounterCode(address, counter))
	copy(code[64:], timerPeriodCode(address+64, timerStartAddress, 0xb3347c, timerStartOriginal))
	copy(code[128:], timerPeriodCode(address+128, timerEndAddress, 0xb33474, timerEndOriginal))
	var written uintptr
	ok, _, err := kernel.NewProc("WriteProcessMemory").Call(uintptr(process), address, uintptr(unsafe.Pointer(&code[0])), uintptr(len(code)), uintptr(unsafe.Pointer(&written)))
	if ok == 0 || written != uintptr(len(code)) {
		return 0, fmt.Errorf("write FPS observer: %w", err)
	}
	var old uint32
	ok, _, err = kernel.NewProc("VirtualProtectEx").Call(uintptr(process), address, performancePageSize, 0x20, uintptr(unsafe.Pointer(&old)))
	if ok == 0 {
		return 0, err
	}
	ok, _, err = kernel.NewProc("FlushInstructionCache").Call(uintptr(process), address, uintptr(len(code)))
	if ok == 0 {
		return 0, err
	}
	if options.HighFrameRate {
		// Request finer Sleep precision inside the game process, paired on normal
		// loop exit. This changes scheduling resolution, never the time scale.
		for _, p := range []struct {
			entry, code uintptr
			original    []byte
		}{
			{timerStartAddress, address + 64, timerStartOriginal}, {timerEndAddress, address + 128, timerEndOriginal},
		} {
			patch := relativeJump(p.entry, p.code)
			for len(patch) < len(p.original) {
				patch = append(patch, 0x90)
			}
			if err := patchClientMemory(process, p.entry, p.original, patch); err != nil {
				return 0, err
			}
		}
	}
	if options.ShowFPS {
		patch := append(relativeJump(presentReturnAddress, address), 0x90, 0x90, 0x90)
		if err := patchClientMemory(process, presentReturnAddress, presentReturnOriginal, patch); err != nil {
			return 0, err
		}
	} else {
		counter = 0
	}
	return counter, nil
}

// gfxz.dat settings read/clamp and save, verified as complete instruction blocks.
// Applied only to a suspended child; disk bytes and normal/mode-1 remain unchanged.
var unlimitedFramePatches = []struct {
	address               uintptr
	original, replacement []byte
}{
	{0xa39990, []byte{0xe8, 0xa4, 0xc7, 0xc2, 0xff, 0x83, 0xc4, 0x04, 0x8b, 0x8d, 0x8c, 0xfc, 0xff, 0xff, 0x89, 0x81, 0xd0, 0x00, 0x00, 0x00, 0x8b, 0x95, 0x8c, 0xfc, 0xff, 0xff, 0x83, 0xba, 0xd0, 0x00, 0x00, 0x00, 0x01, 0x7d, 0x10, 0x8b, 0x85, 0x8c, 0xfc, 0xff, 0xff, 0xc7, 0x80, 0xd0, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}, []byte{0x31, 0xc0, 0x90, 0x90, 0x90, 0x83, 0xc4, 0x04, 0x8b, 0x8d, 0x8c, 0xfc, 0xff, 0xff, 0x89, 0x81, 0xd0, 0x00, 0x00, 0x00, 0x8b, 0x95, 0x8c, 0xfc, 0xff, 0xff, 0x83, 0xba, 0xd0, 0x00, 0x00, 0x00, 0x01, 0xeb, 0x10, 0x8b, 0x85, 0x8c, 0xfc, 0xff, 0xff, 0xc7, 0x80, 0xd0, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}},
	{0xa3e08f, []byte{0xc7, 0x45, 0xfc, 0x00, 0x00, 0x00, 0x00, 0x8b, 0x55, 0xe8, 0x52, 0x68, 0xc8, 0xb2, 0xb9, 0x00}, []byte{0xc7, 0x45, 0xfc, 0x00, 0x00, 0x00, 0x00, 0x31, 0xd2, 0x90, 0x52, 0x68, 0xc8, 0xb2, 0xb9, 0x00}},
}

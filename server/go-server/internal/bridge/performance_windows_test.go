package bridge

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestPresentCounterCode(t *testing.T) {
	const codeAddress = uintptr(0x20000000)
	const counterAddress = codeAddress + performancePageSize
	code := presentCounterCode(codeAddress, counterAddress)
	if len(code) != 24 || !bytes.Equal(code[:7], []byte{0x85, 0xc0, 0x75, 7, 0xf0, 0xff, 5}) || binary.LittleEndian.Uint32(code[7:11]) != uint32(counterAddress) {
		t.Fatal("invalid success-only counter instructions")
	}
	if !bytes.Equal(code[11:19], presentReturnOriginal) {
		t.Fatal("original flags/register instructions not preserved")
	}
	destination := uint32(codeAddress) + uint32(len(code)) + binary.LittleEndian.Uint32(code[20:])
	if destination != uint32(presentReturnAddress)+uint32(len(presentReturnOriginal)) {
		t.Fatal("wrong continuation")
	}
	// Optional fixture consumed by the native x86 register/flags execution test.
	if path := os.Getenv("KFO_FRAME_CODE_FILE"); path != "" {
		if err := os.WriteFile(path, code, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPerformanceClientSmoke(t *testing.T) {
	image := os.Getenv("KFO_FRAME_SMOKE_IMAGE")
	if image == "" {
		t.Skip("explicit local client startup test only")
	}
	for _, high := range []bool{false, true} {
		t.Run(fmtBool(high), func(t *testing.T) {
			child, err := startSharedClient(image, PerformanceOptions{HighFrameRate: high, ShowFPS: true})
			if err != nil {
				t.Fatal(err)
			}
			checkOverlay := func() {}
			if overlay := os.Getenv("KFO_FRAME_OVERLAY_EXE"); overlay != "" {
				cmd := exec.Command(overlay, "--fps-overlay", strconv.Itoa(int(child.identity.PID)), strconv.FormatUint(child.identity.Created, 10), strconv.FormatUint(uint64(child.fpsCounter), 10))
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				if err := cmd.Start(); err != nil {
					child.process.Kill()
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				t.Cleanup(func() {
					select {
					case err := <-done:
						if err != nil {
							t.Error("FPS observer exited with error", err)
						}
					case <-time.After(5 * time.Second):
						cmd.Process.Kill()
						t.Error("FPS observer did not exit with game")
					}
				})
				checkOverlay = func() {
					select {
					case err := <-done:
						done <- err
						t.Fatal("FPS observer exited before game", err)
					default:
					}
				}
			}
			t.Cleanup(func() {
				child.process.Kill()
				select {
				case <-child.done:
				case <-time.After(5 * time.Second):
				}
			})
			handle, _, err := kernel.NewProc("OpenProcess").Call(0x10, 0, uintptr(child.identity.PID))
			if handle == 0 {
				t.Fatal(err)
			}
			defer syscall.CloseHandle(syscall.Handle(handle))
			var previous uint32
			var read uintptr
			started := time.Now()
			sampled := time.Time{}
			for time.Since(started) < 25*time.Second {
				checkOverlay()
				select {
				case <-child.done:
					t.Fatal("client exited before FPS observation")
				default:
				}
				var count uint32
				ok, _, err := kernel.NewProc("ReadProcessMemory").Call(handle, child.fpsCounter, uintptr(unsafe.Pointer(&count)), 4, uintptr(unsafe.Pointer(&read)))
				if ok == 0 || read != 4 {
					t.Fatal(err)
				}
				if sampled.IsZero() && count > 5 {
					sampled = time.Now()
					previous = count
				}
				if !sampled.IsZero() && time.Since(sampled) >= 3*time.Second {
					fps := float64(count-previous) / time.Since(sampled).Seconds()
					if fps <= 0 {
						t.Fatal("no successful Present calls")
					}
					t.Logf("high=%t observed_present_fps=%.1f pid=%d", high, fps, child.identity.PID)
					var interval uint32
					kernel.NewProc("ReadProcessMemory").Call(handle, 0x1df9974, uintptr(unsafe.Pointer(&interval)), 4, uintptr(unsafe.Pointer(&read)))
					t.Logf("D3D PresentationInterval=%#x", interval)
					var settings uint32
					kernel.NewProc("ReadProcessMemory").Call(handle, 0x17c86f8, uintptr(unsafe.Pointer(&settings)), 4, uintptr(unsafe.Pointer(&read)))
					values := [2]uint32{}
					kernel.NewProc("ReadProcessMemory").Call(handle, uintptr(settings)+0xd0, uintptr(unsafe.Pointer(&values[0])), 8, uintptr(unsafe.Pointer(&read)))
					var instruction [3]byte
					kernel.NewProc("ReadProcessMemory").Call(handle, frameIntervalAddress, uintptr(unsafe.Pointer(&instruction[0])), 3, uintptr(unsafe.Pointer(&read)))
					t.Logf("render intervals=%v instruction=%x", values, instruction)
					if overlay := os.Getenv("KFO_FRAME_OVERLAY_EXE"); overlay != "" {
						trace, err := os.ReadFile(filepath.Join(filepath.Dir(overlay), "fps-overlay.log"))
						if err != nil || !strings.Contains(string(trace), "display_visible game="+strconv.Itoa(int(child.identity.PID))+" FPS") {
							t.Fatalf("FPS observer started but never displayed a nonzero sample: %v", err)
						}
					}

					return
				}
				time.Sleep(100 * time.Millisecond)
			}
			t.Fatal("no successful Present observed within 25 seconds")
		})
	}
}

func fmtBool(high bool) string {
	if high {
		return "high"
	}
	return "normal"
}

// Exercise real process memory without running any game instruction or login.
func TestUnlimitedFrameSuspendedClient(t *testing.T) {
	image := os.Getenv("KFO_FRAME_PATCH_IMAGE")
	if image == "" {
		t.Skip("requires local gfxz.dat fixture")
	}
	before, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"normal", "mode1", "mode2", "normal_again"} {
		t.Run(mode, func(t *testing.T) {
			app, _ := syscall.UTF16PtrFromString(image)
			dir, _ := syscall.UTF16PtrFromString(filepath.Dir(image))
			si := syscall.StartupInfo{}
			si.Cb = uint32(unsafe.Sizeof(si))
			var pi syscall.ProcessInformation
			if err := syscall.CreateProcess(app, nil, nil, nil, false, 4, nil, dir, &si, &pi); err != nil {
				t.Fatal(err)
			}
			defer syscall.CloseHandle(pi.Process)
			defer syscall.CloseHandle(pi.Thread)
			defer syscall.TerminateProcess(pi.Process, 0)
			_, err := patchPerformance(pi.Process, PerformanceOptions{HighFrameRate: mode == "mode1", UnlimitedFrameRate: mode == "mode2"})
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range unlimitedFramePatches {
				want := p.original
				if mode == "mode2" {
					want = p.replacement
				}
				got := make([]byte, len(want))
				var n uintptr
				ok, _, err := kernel.NewProc("ReadProcessMemory").Call(uintptr(pi.Process), p.address, uintptr(unsafe.Pointer(&got[0])), uintptr(len(got)), uintptr(unsafe.Pointer(&n)))
				if ok == 0 || n != uintptr(len(got)) || !bytes.Equal(got, want) {
					t.Fatalf("%x: %x %v", p.address, got, err)
				}
			}
			if mode == "mode2" {
				if _, err := patchPerformance(pi.Process, PerformanceOptions{UnlimitedFrameRate: true}); err == nil {
					t.Fatal("unexpected bytes must be rejected")
				}
			}
		})
	}
	after, err := os.ReadFile(image)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("disk image changed", err)
	}
}

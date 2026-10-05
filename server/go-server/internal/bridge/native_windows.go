//go:build windows

package bridge

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")
var network = syscall.NewLazyDLL("iphlpapi.dll")
var errIdentity = errors.New("native client identity unavailable")

// ActivateExistingClient makes repeated launcher clicks idempotent. Match the
// full verified client path, never an unrelated program with the same filename.
func ActivateExistingClient(image string) (bool, error) {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, err
	}
	defer syscall.CloseHandle(snapshot)
	entry := syscall.ProcessEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = syscall.Process32First(snapshot, &entry); err == nil; err = syscall.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(syscall.UTF16ToString(entry.ExeFile[:]), filepath.Base(image)) {
			continue
		}
		if _, identityErr := processIdentity(entry.ProcessID, image); identityErr != nil {
			continue
		}
		user := syscall.NewLazyDLL("user32.dll")
		callback := syscall.NewCallback(func(window, parameter uintptr) uintptr {
			var owner uint32
			user.NewProc("GetWindowThreadProcessId").Call(window, uintptr(unsafe.Pointer(&owner)))
			visible, _, _ := user.NewProc("IsWindowVisible").Call(window)
			if owner != entry.ProcessID || visible == 0 {
				return 1
			}
			iconic, _, _ := user.NewProc("IsIconic").Call(window)
			if iconic != 0 {
				user.NewProc("ShowWindow").Call(window, 9)
			}
			user.NewProc("SetForegroundWindow").Call(window)
			return 0
		})
		user.NewProc("EnumWindows").Call(callback, 0)
		return true, nil
	}
	if err != syscall.ERROR_NO_MORE_FILES {
		return false, err
	}
	return false, nil
}

func AcquireLauncher(loginPort int, directory ...string) (syscall.Handle, bool, error) {
	key := fmt.Sprintf(`Local\KungFuOnlineBridge%d`, loginPort)
	if len(directory) > 0 && directory[0] != "" {
		key = fmt.Sprintf(`Local\KungFuOnlineBridge-%x`, sha256.Sum256([]byte(strings.ToLower(filepath.Clean(directory[0])))))
	}
	name, _ := syscall.UTF16PtrFromString(key)
	handle, _, err := kernel.NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return 0, false, err
	}
	return syscall.Handle(handle), err == syscall.ERROR_ALREADY_EXISTS, nil
}

type Identity struct {
	PID     uint32
	Created uint64
	Image   string
}

func processIdentity(pid uint32, expectedImage string) (Identity, error) {
	handle, _, openErr := kernel.NewProc("OpenProcess").Call(0x1000, 0, uintptr(pid))
	if handle == 0 {
		return Identity{}, fmt.Errorf("open client pid=%d: %w", pid, openErr)
	}
	defer kernel.NewProc("CloseHandle").Call(handle)
	name := make([]uint16, 32768)
	length := uint32(len(name))
	result, _, queryErr := kernel.NewProc("QueryFullProcessImageNameW").Call(handle, 0, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&length)))
	if result == 0 {
		return Identity{}, fmt.Errorf("query client path pid=%d: %w", pid, queryErr)
	}
	actualImage := syscall.UTF16ToString(name[:length])
	if !strings.EqualFold(filepath.Clean(actualImage), filepath.Clean(expectedImage)) {
		actual, actualErr := os.Stat(actualImage)
		expected, expectedErr := os.Stat(expectedImage)
		if actualErr != nil || expectedErr != nil || !os.SameFile(actual, expected) {
			return Identity{}, fmt.Errorf("client image mismatch pid=%d actual=%q expected=%q actual_error=%v expected_error=%v", pid, actualImage, expectedImage, actualErr, expectedErr)
		}
	}
	var created, exited, kernelTime, userTime syscall.Filetime
	result, _, _ = kernel.NewProc("GetProcessTimes").Call(handle, uintptr(unsafe.Pointer(&created)), uintptr(unsafe.Pointer(&exited)), uintptr(unsafe.Pointer(&kernelTime)), uintptr(unsafe.Pointer(&userTime)))
	if result == 0 {
		return Identity{}, errIdentity
	}
	return Identity{PID: pid, Created: uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), Image: expectedImage}, nil
}

func endpointTable(udp bool) ([]byte, int, error) {
	name, kind, width := "GetExtendedTcpTable", uintptr(5), 24
	if udp {
		name, kind, width = "GetExtendedUdpTable", 1, 12
	}
	procedure := network.NewProc(name)
	var length uint32
	result, _, _ := procedure.Call(0, uintptr(unsafe.Pointer(&length)), 0, 2, kind, 0)
	if result != 0 && result != 122 {
		return nil, 0, errIdentity
	}
	for attempt := 0; attempt < 3; attempt++ {
		if length < 4 || length > 16*1024*1024 {
			return nil, 0, errIdentity
		}
		buffer := make([]byte, length)
		result, _, _ = procedure.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&length)), 0, 2, kind, 0)
		if result == 122 {
			continue
		}
		if result != 0 || len(buffer) < 4 {
			return nil, 0, errIdentity
		}
		count := int(binary.LittleEndian.Uint32(buffer))
		if 4+count*width > len(buffer) {
			return nil, 0, errIdentity
		}
		return buffer[4 : 4+count*width], width, nil
	}
	return nil, 0, errIdentity
}

func tcpIdentity(connection net.Conn, image string) (Identity, error) {
	peer, peerOK := connection.RemoteAddr().(*net.TCPAddr)
	local, localOK := connection.LocalAddr().(*net.TCPAddr)
	if !peerOK || !localOK || !peer.IP.Equal(net.IPv4(127, 0, 0, 1)) || !local.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		return Identity{}, errIdentity
	}
	table, width, err := endpointTable(false)
	if err != nil {
		return Identity{}, err
	}
	var owner uint32
	for offset := 0; offset < len(table); offset += width {
		row := table[offset : offset+width]
		if binary.LittleEndian.Uint32(row) != 5 || binary.LittleEndian.Uint32(row[4:]) != 0x0100007f || binary.LittleEndian.Uint32(row[12:]) != 0x0100007f || int(binary.BigEndian.Uint16(row[8:])) != peer.Port || int(binary.BigEndian.Uint16(row[16:])) != local.Port {
			continue
		}
		pid := binary.LittleEndian.Uint32(row[20:])
		if owner != 0 && owner != pid {
			return Identity{}, errIdentity
		}
		owner = pid
	}
	return processIdentity(owner, image)
}

func udpIdentity(peer *net.UDPAddr, image string) (Identity, error) {
	if !peer.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		return Identity{}, errIdentity
	}
	table, width, err := endpointTable(true)
	if err != nil {
		return Identity{}, err
	}
	var owner uint32
	for offset := 0; offset < len(table); offset += width {
		row := table[offset : offset+width]
		address := binary.LittleEndian.Uint32(row)
		if (address != 0 && address != 0x0100007f) || int(binary.BigEndian.Uint16(row[4:])) != peer.Port {
			continue
		}
		pid := binary.LittleEndian.Uint32(row[8:])
		if owner != 0 && owner != pid {
			return Identity{}, errIdentity
		}
		owner = pid
	}
	return processIdentity(owner, image)
}

func tablesReady(identity Identity) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		current, err := processIdentity(identity.PID, identity.Image)
		if err != nil || current != identity {
			return errIdentity
		}
		handle, _, _ := kernel.NewProc("OpenProcess").Call(0x1010, 0, uintptr(identity.PID))
		if handle == 0 {
			return errIdentity
		}
		ready := true
		for _, address := range []uintptr{0x17c86f0, 0x17c8a28} {
			var value uint32
			var read uintptr
			result, _, _ := kernel.NewProc("ReadProcessMemory").Call(handle, address, uintptr(unsafe.Pointer(&value)), 4, uintptr(unsafe.Pointer(&read)))
			ready = ready && result != 0 && read == 4 && value >= 0x10000
		}
		kernel.NewProc("CloseHandle").Call(handle)
		if ready {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("native tables not ready")
}

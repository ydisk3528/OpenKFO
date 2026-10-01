//go:build windows

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")
var user = syscall.NewLazyDLL("user32.dll")
var crypt = syscall.NewLazyDLL("crypt32.dll")

const defaultKey = "LS1KuGmfVgqfuRT2"
const processQuery = 0x1000
const wmSetText = 0x000c
const accountControl = 1001
const passwordControl = 1002

type request struct {
	Port                                            int
	Protocol                                        string
	Op, Path, Key, Account, Password, Image, Action string
	Accounts                                        []credentials
	PID                                             uint32
	Created                                         uint64
}
type credentials struct{ Account, Password string }

// LoginSkin.dll accepts eight fixed UTF-16 pairs through WM_COPYDATA.
// Passwords travel only in process memory, never command lines or files.
func accountChoicesPayload(accounts []credentials) ([]byte, error) {
	const maxAccounts, fieldChars = 8, 128
	if len(accounts) > maxAccounts {
		return nil, errors.New("保存的账号数量超过限制")
	}
	data := make([]byte, 4+maxAccounts*fieldChars*4)
	binary.LittleEndian.PutUint32(data, uint32(len(accounts)))
	for i, account := range accounts {
		for j, value := range []string{account.Account, account.Password} {
			units := utf16.Encode([]rune(value))
			if len(units) >= fieldChars || strings.ContainsRune(value, 0) {
				zeroBytes(data)
				return nil, errors.New("保存的账号或密码长度无效")
			}
			for k, unit := range units {
				binary.LittleEndian.PutUint16(data[4+i*fieldChars*4+j*fieldChars*2+k*2:], unit)
			}
		}
	}
	return data, nil
}

type envelope struct {
	Version                            int
	WrappedKey, Nonce, Tag, Ciphertext []byte
}
type blob struct {
	Size uint32
	Data *byte
}

func utf(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func protect(data []byte, decrypt bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("账号记录为空")
	}
	in := blob{uint32(len(data)), &data[0]}
	var out blob
	name := "CryptProtectData"
	if decrypt {
		name = "CryptUnprotectData"
	}
	ok, _, err := crypt.NewProc(name).Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	if ok == 0 {
		return nil, err
	}
	defer kernel.NewProc("LocalFree").Call(uintptr(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
func load(path string) (credentials, error) {
	var result credentials
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var plain []byte
	if len(b) > 0 && b[0] == '{' {
		var e envelope
		if err = json.Unmarshal(b, &e); err != nil {
			return result, err
		}
		if e.Version != 1 {
			return result, errors.New("账号记录版本不支持")
		}
		key, err := protect(e.WrappedKey, true)
		if err != nil {
			return result, err
		}
		block, err := aes.NewCipher(key)
		zeroBytes(key)
		if err != nil {
			return result, err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return result, err
		}
		if len(e.Nonce) != gcm.NonceSize() {
			return result, errors.New("账号记录随机数无效")
		}
		plain, err = gcm.Open(nil, e.Nonce, append(e.Ciphertext, e.Tag...), nil)
		if err != nil {
			return result, err
		}
	} else {
		plain, err = protect(b, true)
		if err != nil {
			return result, err
		}
	}
	defer zeroBytes(plain)
	err = json.Unmarshal(plain, &result)
	return result, err
}
func save(r request) error {
	if r.Account == "" && r.Password == "" {
		err := os.Remove(r.Path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	key := r.Key
	if key == "" {
		key = defaultKey
	}
	if len(key) != 16 {
		return errors.New("账号保存密钥长度无效")
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return errors.New("账号保存密钥无效")
		}
	}
	plain, _ := json.Marshal(credentials{r.Account, r.Password})
	defer zeroBytes(plain)
	block, _ := aes.NewCipher([]byte(key))
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	encrypted := gcm.Seal(nil, nonce, plain, nil)
	wrapped, err := protect([]byte(key), false)
	if err != nil {
		return err
	}
	n := len(encrypted) - gcm.Overhead()
	data, _ := json.Marshal(envelope{1, wrapped, nonce, encrypted[n:], encrypted[:n]})
	return atomic(r.Path, data)
}
func atomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".launcher-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok, _, err := kernel.NewProc("MoveFileExW").Call(uintptr(unsafe.Pointer(utf(name))), uintptr(unsafe.Pointer(utf(path))), 0x1|0x8)
	if ok == 0 {
		return err
	}
	return nil
}
func identity(r request) (syscall.Handle, error) {
	access := uint32(processQuery | syscall.SYNCHRONIZE)
	if r.Op == "stop_port_owner" {
		access |= syscall.PROCESS_TERMINATE
	}
	h, err := syscall.OpenProcess(access, false, r.PID)
	if err != nil {
		return 0, err
	}
	var c, x, k, u syscall.Filetime
	if err = syscall.GetProcessTimes(h, &c, &x, &k, &u); err != nil {
		syscall.CloseHandle(h)
		return 0, err
	}
	created := uint64(c.HighDateTime)<<32 | uint64(c.LowDateTime)
	var buf [32768]uint16
	n := uint32(len(buf))
	ok, _, err := kernel.NewProc("QueryFullProcessImageNameW").Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if ok == 0 || (r.Op != "info" && (created != r.Created || !strings.EqualFold(filepath.Clean(syscall.UTF16ToString(buf[:n])), filepath.Clean(r.Image)))) {
		syscall.CloseHandle(h)
		return 0, errors.New("游戏进程已变化")
	}
	return h, nil
}
func class(h uintptr) string {
	var b [128]uint16
	user.NewProc("GetClassNameW").Call(h, uintptr(unsafe.Pointer(&b[0])), 128)
	return syscall.UTF16ToString(b[:])
}
func gameWindow(pid uint32) uintptr {
	var found uintptr
	cb := syscall.NewCallback(func(h, p uintptr) uintptr {
		var owner uint32
		user.NewProc("GetWindowThreadProcessId").Call(h, uintptr(unsafe.Pointer(&owner)))
		if owner == pid && class(h) == "GAMECLIENT" {
			found = h
			return 0
		}
		return 1
	})
	user.NewProc("EnumWindows").Call(cb, 0)
	return found
}
func window(r request) (any, error) {
	h, err := identity(r)
	if err != nil {
		return false, nil
	}
	defer syscall.CloseHandle(h)
	w := gameWindow(r.PID)
	if r.Action == "status" {
		return true, nil
	}
	if w == 0 {
		return false, nil
	}
	if r.Action == "show" {
		user.NewProc("ShowWindow").Call(w, 9)
		user.NewProc("SetForegroundWindow").Call(w)
		return true, nil
	}
	if r.Action == "fill" {
		choices, err := accountChoicesPayload(r.Accounts)
		if err != nil {
			return false, err
		}
		defer zeroBytes(choices)
		var dialog uintptr
		cb := syscall.NewCallback(func(h, p uintptr) uintptr {
			if class(h) == "LoginChildWndClass" {
				dialog = h
				return 0
			}
			return 1
		})
		user.NewProc("EnumChildWindows").Call(w, cb, 0)
		if dialog == 0 {
			return false, nil
		}
		for id, text := range map[int]string{accountControl: r.Account, passwordControl: r.Password} {
			control, _, _ := user.NewProc("GetDlgItem").Call(dialog, uintptr(id))
			if class(control) != "Edit" {
				return false, nil
			}
			var result uintptr
			ok, _, _ := user.NewProc("SendMessageTimeoutW").Call(control, wmSetText, 0, uintptr(unsafe.Pointer(utf(text))), 3, 1500, uintptr(unsafe.Pointer(&result)))
			if ok == 0 {
				return false, nil
			}
		}
		packet := struct {
			Kind uintptr
			Size uint32
			Data uintptr
		}{0x4b4b4131, uint32(len(choices)), uintptr(unsafe.Pointer(&choices[0]))}
		var accepted uintptr
		ok, _, _ := user.NewProc("SendMessageTimeoutW").Call(dialog, 0x004a, 0, uintptr(unsafe.Pointer(&packet)), 3, 1500, uintptr(unsafe.Pointer(&accepted)))
		return ok != 0 && accepted != 0, nil
	}
	return false, errors.New("未知窗口操作")
}
func run(r request) (any, error) {
	switch r.Op {
	case "port_owners":
		return portOwners()
	case "stop_port_owner":
		return nil, stopPortOwner(r)
	case "info":
		h, e := identity(r)
		if e != nil {
			return nil, e
		}
		defer syscall.CloseHandle(h)
		var c, x, k, u syscall.Filetime
		syscall.GetProcessTimes(h, &c, &x, &k, &u)
		return uint64(c.HighDateTime)<<32 | uint64(c.LowDateTime), nil
	case "load":
		return load(r.Path)
	case "save":
		return nil, save(r)
	case "window":
		return window(r)
	default:
		return nil, errors.New("未知操作")
	}
}
func main() {
	if len(os.Args) == 1 && strings.HasPrefix(filepath.Base(os.Args[0]), "启动器更新修复") {
		exe, err := os.Executable()
		if err == nil {
			err = repairPendingUpdate(filepath.Dir(exe))
		}
		if err != nil {
			user.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(utf(err.Error()))), uintptr(unsafe.Pointer(utf("更新修复"))), 0x10)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--apply" {
		if len(os.Args) != 3 {
			os.Exit(2)
		}
		if err := applyPlan(os.Args[2]); err != nil {
			_ = os.WriteFile(filepath.Join(filepath.Dir(os.Args[2]), "update-error.txt"), []byte(time.Now().Format(time.RFC3339)+"\n"+err.Error()+"\n"), 0600)
			user.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(utf(err.Error()))), uintptr(unsafe.Pointer(utf("更新失败"))), 0x10)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--fps" {
		if err := fps(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return
	}
	var r request
	err := json.NewDecoder(io.LimitReader(os.Stdin, 1024*1024)).Decode(&r)
	var result any
	if err == nil {
		result, err = run(r)
	}
	response := map[string]any{"ok": err == nil, "result": result}
	if err != nil {
		response["error"] = err.Error()
	}
	json.NewEncoder(os.Stdout).Encode(response)
}

var _ = time.Second

// Explicit loop keeps credential clearing compatible with Go 1.20.
func zeroBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

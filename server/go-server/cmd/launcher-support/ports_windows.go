//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type portOwner struct {
	PID      uint32
	Port     int
	Protocol string
	Name     string
	Image    string
	Created  uint64
	CanStop  bool
}

func portOwners(ports ...int) ([]portOwner, error) {
	if len(ports) == 0 {
		ports = []int{18084, 18000, 18001}
	}
	if len(ports) > 16 {
		return nil, errors.New("端口数量无效")
	}
	var values []string
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return nil, errors.New("端口必须为1至65535")
		}
		values = append(values, strconv.Itoa(port))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	script := `$ErrorActionPreference='Stop'; $rows=@(); foreach($t in @('TCP','UDP')) { if($t -eq 'TCP') {$all=Get-NetTCPConnection | Where-Object State -eq Listen} else {$all=Get-NetUDPEndpoint}; foreach($r in $all) {if($r.LocalPort -in @(18084,18000,18001) -and $r.LocalAddress -in @('127.0.0.1','0.0.0.0','::','::1')) {$rows+=@{PID=[int]$r.OwningProcess;Port=[int]$r.LocalPort;Protocol=$t}}}}; ConvertTo-Json -Compress -InputObject @($rows)`
	script = strings.Replace(script, "@(18084,18000,18001)", "@("+strings.Join(values, ",")+")", 1)
	cmd := exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	data, err := cmd.Output()
	if err != nil {
		return nil, errors.New("无法检查端口占用，请稍后重试或使用任务管理器检查登录组件")
	}
	rows := []portOwner{}
	if err = json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &rows); err != nil {
		return nil, errors.New("端口检查结果无效")
	}
	for i := range rows {
		r := &rows[i]
		r.Name = "无法读取名称"
		h, e := syscall.OpenProcess(processQuery|syscall.SYNCHRONIZE, false, r.PID)
		if e != nil {
			continue
		}
		var c, x, k, u syscall.Filetime
		var buf [32768]uint16
		n := uint32(len(buf))
		ok, _, _ := kernel.NewProc("QueryFullProcessImageNameW").Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
		if ok != 0 && syscall.GetProcessTimes(h, &c, &x, &k, &u) == nil {
			r.Image = syscall.UTF16ToString(buf[:n])
			r.Name = filepath.Base(r.Image)
			r.Created = uint64(c.HighDateTime)<<32 | uint64(c.LowDateTime)
			r.CanStop = r.PID > 4 && r.PID != uint32(os.Getpid()) && !strings.HasPrefix(strings.ToLower(filepath.Clean(r.Image)), strings.ToLower(filepath.Clean(os.Getenv("SystemRoot")))+string(filepath.Separator))
		}
		syscall.CloseHandle(h)
	}
	return rows, nil
}
func stopPortOwner(r request) error {
	rows, err := portOwners(r.Port)
	if err != nil {
		return err
	}
	allowed := false
	for _, p := range rows {
		if p.PID == r.PID && p.Created == r.Created && p.Port == r.Port && p.Protocol == r.Protocol && strings.EqualFold(p.Image, r.Image) && p.CanStop {
			allowed = true
			break
		}
	}
	if !allowed {
		return errors.New("占用程序已变化、已退出或属于系统进程，请重新检查；未结束任何程序")
	}
	h, err := identity(r)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	if err = syscall.TerminateProcess(h, 1); err != nil {
		return errors.New("无法结束占用程序，请保存工作后手动关闭该程序")
	}
	syscall.WaitForSingleObject(h, 5000)
	return nil
}

//go:build windows

package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"unsafe"

	"kungfu.local/server/internal/bridge"
)

func main() {
	executable, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	configPath := flag.String("config", filepath.Join(filepath.Dir(executable), "bridge.json"), "online client configuration")
	launch := flag.Bool("launch", true, "start native client")
	window := flag.Int("window", 1, "shared client window number")
	flag.Parse()
	config, configErr := bridge.LoadConfig(*configPath)
	logDirectory := filepath.Dir(executable)
	if configErr == nil && config.SharedClient && config.ControlDirectory != "" {
		logDirectory = config.ControlDirectory
	}
	logFile, err := os.OpenFile(filepath.Join(logDirectory, "online-client.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatal(err)
	}
	defer logFile.Close()
	log.SetOutput(bridge.PrivateLogWriter{Writer: logFile})
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	err = configErr
	if err == nil && config.SharedClient {
		err = bridge.RequestSharedWindow(config, *window)
	}
	if err == nil {
		if *launch && !config.SharedClient {
			var active bool
			var image string
			image, err = config.ImagePath()
			if err == nil {
				active, err = bridge.ActivateExistingClient(image)
			}
			if err == nil && active {
				log.Print("existing client activated; duplicate launch skipped")
				return
			}
		}
	}
	if err == nil {
		launcher, alreadyRunning, lockErr := bridge.AcquireLauncher(config.LoginPort, config.ControlDirectory)
		if lockErr != nil {
			err = lockErr
		} else {
			defer syscall.CloseHandle(launcher)
			if alreadyRunning {
				log.Print("launcher already starting; duplicate launch skipped")
				return
			}
		}
	}
	if err == nil {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		err = bridge.Run(ctx, config, *launch)
	}
	if err != nil {
		log.Printf("launcher stopped: %v", err)
		message, _ := syscall.UTF16PtrFromString("启动失败：" + bridge.RedactLog(err.Error()) + "\n请查看 online-client.log。")
		title, _ := syscall.UTF16PtrFromString("功夫小子 · 线上测试")
		syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
		os.Exit(1)
	}
}

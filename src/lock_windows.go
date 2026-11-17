package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

func lockGame(media string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(media, ".doblaje-yakuza.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	dll := syscall.NewLazyDLL("kernel32.dll")
	lock := dll.NewProc("LockFileEx")
	unlock := dll.NewProc("UnlockFileEx")
	var position syscall.Overlapped
	ok, _, _ := lock.Call(file.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&position)))
	if ok == 0 {
		file.Close()
		return nil, errors.New("Otro instalador ya está usando esta copia del juego")
	}
	return func() {
		_, _, _ = unlock.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&position)))
		_ = file.Close()
	}, nil
}

func gameRunning() bool {
	cmd := exec.Command("tasklist.exe", "/FI", "IMAGENAME eq "+gameExe, "/FO", "CSV", "/NH")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, _ := cmd.Output()
	return strings.Contains(strings.ToLower(string(output)), "\""+strings.ToLower(gameExe)+"\"")
}

func publishNew(source, destination string) error {
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return syscall.MoveFile(from, to)
}

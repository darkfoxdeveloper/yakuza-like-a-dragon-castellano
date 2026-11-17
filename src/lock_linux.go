package main

import (
	"errors"
	"io"
	"os"
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
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("Otro instalador ya está usando esta copia del juego")
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func gameRunning() bool {
	list, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, path := range list {
		command, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, arg := range strings.Split(string(command), "\x00") {
			name := filepath.Base(strings.ReplaceAll(arg, "\\", "/"))
			if strings.EqualFold(name, gameExe) {
				return true
			}
		}
	}
	return false
}

func publishNew(source, destination string) error {
	from, err := syscall.BytePtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.BytePtrFromString(destination)
	if err != nil {
		return err
	}
	const renameAt2AMD64 = 316
	const noReplace = 1
	workingDirectory := -100 // AT_FDCWD
	_, _, failure := syscall.Syscall6(renameAt2AMD64,
		uintptr(workingDirectory), uintptr(unsafe.Pointer(from)),
		uintptr(workingDirectory), uintptr(unsafe.Pointer(to)), noReplace, 0)
	switch failure {
	case 0:
		return nil
	case syscall.EINVAL, syscall.ENOSYS, syscall.EOPNOTSUPP:
		// Some non-native mounts lack renameat2. Reserve the destination first.
		return publishOnLegacyMount(source, destination)
	default:
		return failure
	}
}

func publishOnLegacyMount(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return publishDirectoryOnLegacyMount(source, destination)
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, writeErr := io.Copy(output, input)
	err = errors.Join(writeErr, output.Sync(), output.Close())
	if err != nil {
		_ = os.Remove(destination)
	}
	return err
}

func publishDirectoryOnLegacyMount(source, destination string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	if err := os.Mkdir(destination, 0755); err != nil {
		return err
	}
	// Record ownership first so a later invocation can repair an interrupted move.
	if err := os.Rename(filepath.Join(source, markerName), filepath.Join(destination, markerName)); err != nil {
		_ = os.Remove(destination)
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == markerName {
			continue
		}
		if err := os.Rename(filepath.Join(source, name), filepath.Join(destination, name)); err != nil {
			return err
		}
	}
	return nil
}

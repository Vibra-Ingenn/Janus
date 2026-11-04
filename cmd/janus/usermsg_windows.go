//go:build windows

package main

import (
	"bufio"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func userAlert(title, message string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBoxW := user32.NewProc("MessageBoxW")
	t16, _ := syscall.UTF16PtrFromString(title)
	m16, _ := syscall.UTF16PtrFromString(message)
	_, _, _ = messageBoxW.Call(0, uintptr(unsafe.Pointer(m16)), uintptr(unsafe.Pointer(t16)), 0)
}

func userAlertAndWait(title, message string) {
	userAlert(title, message)
	fmt.Fprintln(os.Stderr, message)
	fmt.Fprintln(os.Stderr, "Press Enter to close...")
	_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
}

//go:build windows
// +build windows

package main

import (
	"syscall"
	"unsafe"
)

func showErrorBox(title, message string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	
	tPtr, _ := syscall.UTF16PtrFromString(title)
	mPtr, _ := syscall.UTF16PtrFromString(message)
	
	// MB_OK (0x0) | MB_ICONERROR (0x10) | MB_SETFOREGROUND (0x10000)
	messageBox.Call(0, uintptr(unsafe.Pointer(mPtr)), uintptr(unsafe.Pointer(tPtr)), 0x00010010)
}

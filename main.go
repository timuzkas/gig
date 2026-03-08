package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"
)

func main() {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("libgtk-4-1.dll"); err != nil {
			showErrorBox("GTK4 Runtime Missing", 
				"GTK4 runtime was not found on your system.\n\n"+
				"Please download and install GTK4 to run this application:\n"+
				"https://www.gtk.org/docs/installations/windows/\n\n"+
				"Or install via MSYS2: pacman -S mingw-w64-x86_64-gtk4")
			os.Exit(1)
		}
	}

	cfg := LoadConfig()
	SetLogConfig(cfg.Behavior.Logging, cfg.Behavior.LogPath)
	app := NewApp(cfg)
	app.Run()
}

func showErrorBox(title, message string) {
	if runtime.GOOS != "windows" {
		fmt.Fprintf(os.Stderr, "%s: %s\n", title, message)
		return
	}

	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	
	tPtr, _ := syscall.UTF16PtrFromString(title)
	mPtr, _ := syscall.UTF16PtrFromString(message)
	
	// MB_OK (0x0) | MB_ICONERROR (0x10) | MB_SETFOREGROUND (0x10000)
	messageBox.Call(0, uintptr(unsafe.Pointer(mPtr)), uintptr(unsafe.Pointer(tPtr)), 0x00010010)
}

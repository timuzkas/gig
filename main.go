package main

import (
	"os"
	"os/exec"
	"runtime"
	"flag"
)

var configPath string

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

	flag.StringVar(&configPath, "c", "", "Path to config file")
    flag.Parse()

    cfg := LoadConfigFrom(configPath)
	
	SetLogConfig(cfg.Behavior.Logging, cfg.Behavior.LogPath)
	app := NewApp(cfg)
	app.Run()
}


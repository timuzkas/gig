package main

func main() {
	cfg := LoadConfig()
	SetLogConfig(cfg.Behavior.Logging, cfg.Behavior.LogPath)
	app := NewApp(cfg)
	app.Run()
}

package main

func main() {
	cfg := LoadConfig()
	app := NewApp(cfg)
	app.Run()
}

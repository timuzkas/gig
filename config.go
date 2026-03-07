package main

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Appearance AppearanceConfig `toml:"appearance"`
	Colors     ColorsConfig     `toml:"colors"`
	Behavior   BehaviorConfig   `toml:"behavior"`
	Repos      ReposConfig      `toml:"repos"`
}

type AppearanceConfig struct {
	FontFamily       string `toml:"font_family"`
	FontSize         int    `toml:"font_size"`
	SidebarWidth     int    `toml:"sidebar_width"`
	RepoSidebarWidth int    `toml:"repo_sidebar_width"`
	Dark             bool   `toml:"dark"`
}

type ColorsConfig struct {
	Bg        string `toml:"bg"`
	Surface   string `toml:"surface"`
	Surface2  string `toml:"surface_2"`
	Border    string `toml:"border"`
	Text      string `toml:"text"`
	TextDim   string `toml:"text_dim"`
	Accent    string `toml:"accent"`
	Added     string `toml:"added"`
	Removed   string `toml:"removed"`
	Modified  string `toml:"modified"`
	Selection string `toml:"selection"`
}

type BehaviorConfig struct {
	MaxCommits         int  `toml:"max_commits"`
	AutoRefresh        bool `toml:"auto_refresh"`
	RefreshIntervalSec int  `toml:"refresh_interval_sec"`
	ScanParentOnStart  bool `toml:"scan_parent_on_start"`
}

type ReposConfig struct {
	Paths []string `toml:"paths"`
}

func DefaultConfig() Config {
	return Config{
		Appearance: AppearanceConfig{
			FontFamily:       "monospace",
			FontSize:         13,
			SidebarWidth:     280,
			RepoSidebarWidth: 220,
			Dark:             true,
		},
		Colors: ColorsConfig{
			Bg:        "#0b0b0c",
			Surface:   "#121214",
			Surface2:  "#17171a",
			Border:    "#242428",
			Text:      "#e8e8ea",
			TextDim:   "#8b8b93",
			Accent:    "#7aa2f7",
			Added:     "#73daca",
			Removed:   "#f7768e",
			Modified:  "#e0af68",
			Selection: "#1d2a44",
		},
		Behavior: BehaviorConfig{
			MaxCommits:         200,
			AutoRefresh:        true,
			RefreshIntervalSec: 4,
			ScanParentOnStart:  true,
		},
		Repos: ReposConfig{
			Paths: []string{"."},
		},
	}
}

func LoadConfig() Config {
	cfg := DefaultConfig()

	candidates := []string{
		"config.toml",
		filepath.Join(configDir(), "gig4", "config.toml"),
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, &cfg); err == nil {
				return cfg
			}
		}
	}

	return cfg
}

func configDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return d
	}
	return "."
}

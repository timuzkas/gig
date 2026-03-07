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
	Features   FeaturesConfig   `toml:"features"`
	Repos      ReposConfig      `toml:"repos"`
}

type AppearanceConfig struct {
	FontFamily       string `toml:"font_family"`
	FontSize         int    `toml:"font_size"`
	SidebarWidth     int    `toml:"sidebar_width"`
	RepoSidebarWidth int    `toml:"repo_sidebar_width"`
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
	EditorCommand string `toml:"editor_command"`
}

type FeaturesConfig struct {
	AsyncStateReload  bool `toml:"async_state_reload"`
	AsyncDiffLoading  bool `toml:"async_diff_loading"`
	ShowCommitAuthors bool `toml:"show_commit_authors"`
	ShowCommitDates   bool `toml:"show_commit_dates"`
	ShowRemoteSummary bool `toml:"show_remote_summary"`
	RepoConfigDialog  bool `toml:"repo_config_dialog"`
}

type ReposConfig struct {
	Paths []string `toml:"paths"`
}

func DefaultConfig() Config {
	return Config{
		Appearance: AppearanceConfig{
			FontFamily:       "monospace",
			FontSize:         12,
			SidebarWidth:     260,
			RepoSidebarWidth: 200,
		},
		Colors: ColorsConfig{
			Bg:        "#0f0e0d",
			Surface:   "#161513",
			Surface2:  "#1d1b19",
			Border:    "#2a2724",
			Text:      "#ddd8d0",
			TextDim:   "#6e6860",
			Accent:    "#c9955c",
			Added:     "#7ab87a",
			Removed:   "#c06060",
			Modified:  "#b89a5a",
			Selection: "#2b2318",
		},
		Behavior: BehaviorConfig{
			MaxCommits:         200,
			AutoRefresh:        true,
			RefreshIntervalSec: 5,
			ScanParentOnStart:  true,
		},
		Features: FeaturesConfig{
			AsyncStateReload:  true,
			AsyncDiffLoading:  true,
			ShowCommitAuthors: true,
			ShowCommitDates:   true,
			ShowRemoteSummary: true,
			RepoConfigDialog:  true,
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
		filepath.Join(configDir(), "gig", "config.toml"),
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

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
	Hotkeys    HotkeysConfig    `toml:"hotkeys"`
}

type HotkeysConfig struct {
	Refresh      string `toml:"refresh"`
	Commit       string `toml:"commit"`
	Fetch        string `toml:"fetch"`
	Pull         string `toml:"pull"`
	Push         string `toml:"push"`
	Branch       string `toml:"branch"`
	Panel1       string `toml:"panel_1"`
	Panel2       string `toml:"panel_2"`
	Panel3       string `toml:"panel_3"`
	PrevCommit   string `toml:"prev_commit"`
	NextCommit   string `toml:"next_commit"`
	ToggleSplit  string `toml:"toggle_split"`
	Search       string `toml:"search"`
	OpenDir      string `toml:"open_dir"`
	Help string `toml:"help"`
	StageAll string `toml:"stage_all"`
	Diff string `toml:"diff"`
	Edit string `toml:"edit"`
	UnstageAll string `toml:"unstage_all"`
	RevertAll string `toml:"revert_all"`
	PushForce string `toml:"push_force"`
	Sync string `toml:"sync"`
	CopyHash string `toml:"copy_hash"`
	JumpTo string `toml:"jump_to"`
	NewBranch string `toml:"new_branch"`
	Stash string `toml:"stash"`
	StashPop string `toml:"stash_pop"`
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
	MaxCommits         int    `toml:"max_commits"`
	AutoRefresh        bool   `toml:"auto_refresh"`
	RefreshIntervalSec int    `toml:"refresh_interval_sec"`
	ScanParentOnStart  bool   `toml:"scan_parent_on_start"`
	EditorCommand      string `toml:"editor_command"`
	Logging            bool   `toml:"logging"`
	LogPath            string `toml:"log_path"`
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
    Paths        []string `toml:"paths"`
    StarredPaths []string `toml:"starred_paths"`
    StarredOnly  bool     `toml:"starred_only"`
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
			Logging:            false,
			LogPath:            "", // Empty means relative "gig.log" or handled by logic
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
		Hotkeys: HotkeysConfig{
			Refresh:     "<Control>r",
			Commit:      "<Control>Return",
			Fetch:       "<Control>f",
			Pull:        "<Control>l",
			Push:        "<Control>p",
			Branch:      "<Control>b",
			Panel1:      "<Control>1",
			Panel2:      "<Control>2",
			Panel3:      "<Control>3",
			PrevCommit:  "<Control>bracketleft",
			NextCommit:  "<Control>bracketright",
			ToggleSplit: "<Control>t",
			Search:      "slash",
			OpenDir:     "<Control>o",
			Help: "<Control>h",
			StageAll: "<Control>a",
			Diff: "<Control>d",
			Edit: "<Control>e",
			UnstageAll: "<Control>z",
			RevertAll: "<Control><Shift>z",
			PushForce: "<Control><Shift>p",
			Sync: "<Control><Shift>f",
			CopyHash: "<Control><Shift>c",
			JumpTo: "<Control>g",
			NewBranch: "<Control>n",
			Stash: "<Control>s",
			StashPop: "<Control><Shift>s",
		},
	}
}

func LoadConfig() Config {
    return LoadConfigFrom("")
}

func LoadConfigFrom(cfgPath string) Config {
    cfg := DefaultConfig()
    var candidates []string
    if cfgPath != "" {
        candidates = []string{cfgPath}
    } else {
        candidates = []string{
            "config.toml",
            filepath.Join(configDir(), "gig", "config.toml"),
        }
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

func SaveConfig(cfg Config) {
    dir := filepath.Join(configDir(), "gig")
    os.MkdirAll(dir, 0755)
    path := filepath.Join(dir, "config.toml")
    if _, err := os.Stat("config.toml"); err == nil {
        path = "config.toml"
    }
    f, err := os.Create(path)
    if err != nil {
        return
    }
    defer f.Close()
    toml.NewEncoder(f).Encode(cfg)
}

func configDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return d
	}
	return "."
}

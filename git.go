package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Commit struct {
	Hash      string
	ShortHash string
	Subject   string
	Author    string
	Date      time.Time
	DateRel   string
	Graph     string
	RefNames  string
	IsCommit  bool
}

type FileStatus struct {
	Path        string
	IndexStatus string
	WorkStatus  string
	Staged      bool
}

type BranchInfo struct {
	Name    string
	Current bool
	Remote  string
	Hash    string
	Subject string
	Date    string
}

type RemoteInfo struct {
	Name     string
	FetchURL string
	PushURL  string
}

type RepoInfo struct {
	Name string
	Path string
}

type RepoState struct {
	Path     string
	Name     string
	Branch   string
	Commits  []Commit
	Files    []FileStatus
	Branches []BranchInfo
	Remotes  []RemoteInfo
	Ahead    int
	Behind   int
}

func gitCmd(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}

func IsGitRepo(path string) bool {
	_, err := gitCmd(path, "rev-parse", "--git-dir")
	return err == nil
}

func FindRepoRoot(path string) (string, error) {
	out, err := gitCmd(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.Clean(out), nil
}

func GetCurrentBranch(repoPath string) string {
	out, err := gitCmd(repoPath, "branch", "--show-current")
	if err == nil && out != "" {
		return out
	}

	head, err := gitCmd(repoPath, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "unknown"
	}

	return "(detached " + head + ")"
}

func GetBranches(repoPath string) []BranchInfo {
	out, err := gitCmd(
		repoPath,
		"branch",
		"-a",
		"--format=%(HEAD)|%(refname:short)|%(upstream:short)|%(objectname:short)|%(subject)|%(authordate:relative)",
	)
	if err != nil {
		return nil
	}

	var branches []BranchInfo

	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 6)
		if len(parts) < 2 {
			continue
		}

		b := BranchInfo{
			Current: strings.TrimSpace(parts[0]) == "*",
			Name:    strings.TrimSpace(parts[1]),
		}
		if len(parts) > 2 {
			b.Remote = strings.TrimSpace(parts[2])
		}
		if len(parts) > 3 {
			b.Hash = strings.TrimSpace(parts[3])
		}
		if len(parts) > 4 {
			b.Subject = strings.TrimSpace(parts[4])
		}
		if len(parts) > 5 {
			b.Date = strings.TrimSpace(parts[5])
		}

		branches = append(branches, b)
	}

	return branches
}

func GetRemotes(repoPath string) []RemoteInfo {
	out, err := gitCmd(repoPath, "remote")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}

	var remotes []RemoteInfo

	for _, name := range strings.Split(out, "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		fetchURL, _ := gitCmd(repoPath, "remote", "get-url", name)
		pushURL, err := gitCmd(repoPath, "remote", "get-url", "--push", name)
		if err != nil || strings.TrimSpace(pushURL) == "" {
			pushURL = fetchURL
		}

		remotes = append(remotes, RemoteInfo{
			Name:     name,
			FetchURL: strings.TrimSpace(fetchURL),
			PushURL:  strings.TrimSpace(pushURL),
		})
	}

	sort.Slice(remotes, func(i, j int) bool {
		return strings.ToLower(remotes[i].Name) < strings.ToLower(remotes[j].Name)
	})

	return remotes
}

func AddRemote(repoPath, name, url string) error {
	_, err := gitCmd(repoPath, "remote", "add", name, url)
	return err
}

func RemoveRemote(repoPath, name string) error {
	_, err := gitCmd(repoPath, "remote", "remove", name)
	return err
}

func RenameRemote(repoPath, oldName, newName string) error {
	_, err := gitCmd(repoPath, "remote", "rename", oldName, newName)
	return err
}

func SetRemoteURL(repoPath, name, url string) error {
	_, err := gitCmd(repoPath, "remote", "set-url", name, url)
	return err
}

func GetBranchUpstream(repoPath, branch string) string {
	out, err := gitCmd(
		repoPath,
		"for-each-ref",
		"--format=%(upstream:short)",
		"refs/heads/"+branch,
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func SetBranchUpstream(repoPath, branch, upstream string) error {
	_, err := gitCmd(repoPath, "branch", "--set-upstream-to", upstream, branch)
	return err
}


func GetAheadBehind(repoPath string) (int, int) {
	out, err := gitCmd(
		repoPath,
		"rev-list",
		"--left-right",
		"--count",
		"HEAD...@{upstream}",
	)
	if err != nil {
		return 0, 0
	}

	var ahead, behind int
	fmt.Sscanf(out, "%d\t%d", &ahead, &behind)
	return ahead, behind
}

func GetLog(repoPath string, max int) []Commit {
	out, err := gitCmd(
		repoPath,
		"log",
		"--graph",
		"--all",
		fmt.Sprintf("--max-count=%d", max),
		"--date=iso-strict",
		"--format=§%H§%h§%s§%an§%aI§%ar§%D",
	)
	if err != nil {
		return nil
	}

	var commits []Commit

	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.SplitN(line, "§", 8)
		if len(parts) < 8 {
			commits = append(commits, Commit{
				Graph:    line,
				IsCommit: false,
			})
			continue
		}

		graph := parts[0]
		t, _ := time.Parse(time.RFC3339, parts[5])

		commits = append(commits, Commit{
			Hash:      parts[1],
			ShortHash: parts[2],
			Subject:   parts[3],
			Author:    parts[4],
			Date:      t,
			DateRel:   parts[6],
			RefNames:  parts[7],
			Graph:     graph,
			IsCommit:  true,
		})
	}

	return commits
}

func GetStatus(repoPath string) []FileStatus {
	out, err := gitCmd(repoPath, "status", "--porcelain=v1")
	if err != nil {
		return nil
	}

	var files []FileStatus

	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}

		x := string(line[0])
		y := string(line[1])
		path := strings.TrimSpace(line[3:])

		if x != " " {
			files = append(files, FileStatus{
				Path:        path,
				IndexStatus: x,
				WorkStatus:  y,
				Staged:      true,
			})
		}
		if y != " " || x == "?" {
			ws := y
			if x == "?" {
				ws = "?"
			}
			files = append(files, FileStatus{
				Path:        path,
				IndexStatus: x,
				WorkStatus:  ws,
				Staged:      false,
			})
		}
	}

	return files
}

func GetFileDiff(repoPath, filePath string, staged bool) string {
	args := []string{"diff", "--no-color"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", filePath)

	out, err := gitCmd(repoPath, args...)
	if err != nil {
		return ""
	}

	return out
}

func GetCommitDiff(repoPath, hash string) string {
	out, err := gitCmd(repoPath, "show", "--no-color", "--format=medium", hash)
	if err != nil {
		return ""
	}
	return out
}

func StageFile(repoPath, filePath string) error {
	_, err := gitCmd(repoPath, "add", "--", filePath)
	return err
}

func UnstageFile(repoPath, filePath string) error {
	_, err := gitCmd(repoPath, "reset", "HEAD", "--", filePath)
	return err
}

func DoCommit(repoPath, message string) error {
	_, err := gitCmd(repoPath, "commit", "-m", message)
	return err
}

func Checkout(repoPath, branch string) error {
	_, err := gitCmd(repoPath, "checkout", branch)
	return err
}

func Pull(repoPath string) error {
	_, err := gitCmd(repoPath, "pull")
	return err
}

func Push(repoPath string) error {
	_, err := gitCmd(repoPath, "push")
	return err
}

func Fetch(repoPath string) error {
	_, err := gitCmd(repoPath, "fetch", "--all")
	return err
}

func LoadRepoState(repoPath string, maxCommits int) *RepoState {
	root, err := FindRepoRoot(repoPath)
	if err != nil {
		return nil
	}

	ahead, behind := GetAheadBehind(root)

	return &RepoState{
		Path:     root,
		Name:     filepath.Base(root),
		Branch:   GetCurrentBranch(root),
		Commits:  GetLog(root, maxCommits),
		Files:    GetStatus(root),
		Branches: GetBranches(root),
		Remotes:  GetRemotes(root),
		Ahead:    ahead,
		Behind:   behind,
	}
}

func DiscoverRepos(inputs []string, scanParent bool) []RepoInfo {
	seen := map[string]bool{}
	var repos []RepoInfo

	addRepo := func(path string) {
		root, err := FindRepoRoot(path)
		if err != nil {
			return
		}
		if seen[root] {
			return
		}
		seen[root] = true
		repos = append(repos, RepoInfo{
			Name: filepath.Base(root),
			Path: root,
		})
	}

	for _, p := range inputs {
		if p == "" {
			continue
		}

		if IsGitRepo(p) {
			addRepo(p)
			continue
		}

		if !scanParent {
			continue
		}

		entries, err := os.ReadDir(p)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			child := filepath.Join(p, entry.Name())
			if IsGitRepo(child) {
				addRepo(child)
			}
		}
	}

	sort.Slice(repos, func(i, j int) bool {
		return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name)
	})

	return repos
}

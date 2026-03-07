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
		"--format=%(HEAD)|%(refname:short)|%(upstream:short)",
	)
	if err != nil {
		return nil
	}

	var branches []BranchInfo

	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 3)
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

		branches = append(branches, b)
	}

	return branches
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
		fmt.Sprintf("--max-count=%d", max),
		"--date=iso-strict",
		"--format=%H|%h|%s|%an|%aI|%ar",
		"--all",
	)
	if err != nil {
		return nil
	}

	var commits []Commit

	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 6)
		if len(parts) != 6 {
			continue
		}

		t, _ := time.Parse(time.RFC3339, parts[4])

		commits = append(commits, Commit{
			Hash:      parts[0],
			ShortHash: parts[1],
			Subject:   parts[2],
			Author:    parts[3],
			Date:      t,
			DateRel:   parts[5],
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

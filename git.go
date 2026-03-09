package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type LogConfig struct {
	Enabled bool
	Path    string
}

var globalLogConfig LogConfig

func SetLogConfig(enabled bool, path string) {
	globalLogConfig = LogConfig{
		Enabled: enabled,
		Path:    path,
	}
}

type Commit struct {
	Hash        string
	ShortHash   string
	Subject     string
	Author      string
	AuthorEmail string
	Date        time.Time
	DateRel     string
	Graph       string
	RefNames    string
	IsCommit    bool
	Parents     []string
}

type FileStatus struct {
	Path        string
	IndexStatus string
	WorkStatus  string
	Staged      bool
}

type BranchInfo struct {
	Name       string
	Current    bool
	Remote     string
	Hash       string
	Subject    string
	Date       string
	Ahead      int
	Behind     int
	IsRemote   bool
}

type RemoteInfo struct {
	Name     string
	FetchURL string
	PushURL  string
}

type RepoInfo struct {
    Name    string
    Path    string
    Starred bool
}

type StashEntry struct {
	Index   int
	Ref     string
	Message string
	Date    string
}

type RepoState struct {
	Path     string
	Name     string
	Branch   string
	Commits  []Commit
	Files    []FileStatus
	Branches []BranchInfo
	Remotes  []RemoteInfo
	Stashes  []StashEntry
	Ahead    int
	Behind   int
}

type ConflictKind string

const (
	ConflictBothModified  ConflictKind = "UU" // most common
	ConflictBothAdded     ConflictKind = "AA"
	ConflictBothDeleted   ConflictKind = "DD"
	ConflictAddedByUs     ConflictKind = "AU"
	ConflictAddedByThem   ConflictKind = "UA"
	ConflictDeletedByUs   ConflictKind = "DU"
	ConflictDeletedByThem ConflictKind = "UD"
)

type ConflictFile struct {
	Path     string
	Kind     ConflictKind
	Base     string
	Ours     string
	Theirs   string
	Hunks    []ConflictHunk
	Resolved bool
}

type ConflictHunk struct {
	StartLine   int
	OursLines   []string
	BaseLines   []string
	TheirsLines []string
	Resolution  HunkResolution
}

type HunkResolution int

const (
	ResolutionNone HunkResolution = iota
	ResolutionOurs
	ResolutionTheirs
	ResolutionBoth
	ResolutionBothRev
	ResolutionCustom
)

func IsConflicted(repoPath string) bool {
	out, _ := gitCmd(repoPath, "status", "--porcelain=v1")
	for _, line := range strings.Split(out, "\n") {
		if len(line) >= 2 {
			xy := line[:2]
			switch xy {
			case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
				return true
			}
		}
	}
	return false
}

func GetConflictFiles(repoPath string) []ConflictFile {
	out, _ := gitCmd(repoPath, "status", "--porcelain=v1")
	var files []ConflictFile
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		switch xy {
		case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
			path := strings.TrimSpace(line[3:])
			if strings.HasPrefix(path, "\"") && strings.HasSuffix(path, "\"") {
				path = path[1 : len(path)-1]
				path = strings.ReplaceAll(path, "\\\"", "\"")
				path = strings.ReplaceAll(path, "\\\\", "\\")
			}
			files = append(files, ConflictFile{
				Path: path,
				Kind: ConflictKind(xy),
			})
		}
	}
	return files
}

func GetFileVersions(repoPath, path string) (base, ours, theirs string) {
	base, _ = gitCmd(repoPath, "show", ":1:"+path)
	ours, _ = gitCmd(repoPath, "show", ":2:"+path)
	theirs, _ = gitCmd(repoPath, "show", ":3:"+path)
	return
}

func ParseConflictHunks(content string) []ConflictHunk {
	var hunks []ConflictHunk
	lines := strings.Split(content, "\n")

	type state int
	const (
		stateNormal state = iota
		stateOurs
		stateBase
		stateTheirs
	)

	cur := stateNormal
	var hunk ConflictHunk
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "<<<<<<<"):
			cur = stateOurs
			hunk = ConflictHunk{StartLine: i, Resolution: ResolutionNone}
		case strings.HasPrefix(line, "|||||||") && cur == stateOurs:
			cur = stateBase
		case strings.HasPrefix(line, "=======") && (cur == stateOurs || cur == stateBase):
			cur = stateTheirs
		case strings.HasPrefix(line, ">>>>>>>") && cur == stateTheirs:
			hunks = append(hunks, hunk)
			hunk = ConflictHunk{}
			cur = stateNormal
		default:
			switch cur {
			case stateOurs:
				hunk.OursLines = append(hunk.OursLines, line)
			case stateBase:
				hunk.BaseLines = append(hunk.BaseLines, line)
			case stateTheirs:
				hunk.TheirsLines = append(hunk.TheirsLines, line)
			}
		}
	}
	return hunks
}

func MarkResolved(repoPath, path string) error {
	_, err := gitCmd(repoPath, "add", "--", path)
	return err
}

func AbortMerge(repoPath string) error {
	if _, err := gitCmd(repoPath, "merge", "--abort"); err == nil {
		return nil
	}
	if _, err := gitCmd(repoPath, "rebase", "--abort"); err == nil {
		return nil
	}
	if _, err := gitCmd(repoPath, "cherry-pick", "--abort"); err == nil {
		return nil
	}
	return fmt.Errorf("could not abort operation")
}

func ContinueMerge(repoPath string) error {
	_, err := gitCmd(repoPath, "merge", "--continue")
	if err == nil {
		return nil
	}
	// If it's not a merge, try rebase
	_, err2 := gitCmd(repoPath, "rebase", "--continue")
	if err2 == nil {
		return nil
	}
	
	if strings.Contains(err.Error(), "no merge in progress") && !strings.Contains(err2.Error(), "no rebase in progress") {
		return err2
	}
	return err
}

func EnsureDiff3Style(repoPath string) {
	_, _ = gitCmd(repoPath, "config", "merge.conflictstyle", "diff3")
}

func gitCmd(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	stdoutStr := strings.TrimRight(stdout.String(), "\n\r\t ")
	stderrStr := strings.TrimSpace(stderr.String())

	logGitOp(repoPath, args, err, stdoutStr, stderrStr, duration)

	if err != nil {
		if stderrStr != "" {
			return "", fmt.Errorf("%s", stderrStr)
		}
		return "", err
	}

	return stdoutStr, nil
}

func logGitOp(repoPath string, args []string, err error, stdout, stderr string, duration time.Duration) {
	if !globalLogConfig.Enabled {
		return
	}

	logFile := "gig.log"
	if globalLogConfig.Path != "" {
		if globalLogConfig.Path == ".config" {
			if d, err := os.UserConfigDir(); err == nil {
				logFile = filepath.Join(d, "gig", "gig.log")
				os.MkdirAll(filepath.Dir(logFile), 0755)
			}
		} else {
			logFile = globalLogConfig.Path
		}
	}

	f, lerr := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if lerr != nil {
		return
	}
	defer f.Close()

	status := "OK"
	if err != nil {
		status = "ERR"
	}

	cmdStr := "git " + strings.Join(args, " ")
	logLine := fmt.Sprintf("[%s] [%s] [%s] [%v] %s\n",
		time.Now().Format("2006-01-02 15:04:05"),
		status,
		repoPath,
		duration.Round(time.Millisecond),
		cmdStr,
	)
	f.WriteString(logLine)

	if err != nil {
		f.WriteString(fmt.Sprintf("  Error: %v\n", err))
		if stderr != "" {
			f.WriteString(fmt.Sprintf("  Stderr: %s\n", stderr))
		}
	}
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

	out, err = gitCmd(repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err == nil && out != "HEAD" {
		return out
	}

	out, err = gitCmd(repoPath, "rev-parse", "--short", "HEAD")
	if err == nil {
		return "(detached at " + out + ")"
	}

	return "unknown"
}

func GetBranches(repoPath string) []BranchInfo {
	out, err := gitCmd(
		repoPath,
		"branch",
		"-a",
		"--format=%(HEAD)|%(refname:short)|%(upstream:short)|%(objectname:short)|%(subject)|%(authordate:relative)|%(upstream:track,nobracket)",
	)
	if err != nil {
		return nil
	}

	var branches []BranchInfo

	out = strings.ReplaceAll(out, "\r\n", "\n")
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 7)
		if len(parts) < 2 {
			continue
		}

		name := strings.TrimSpace(parts[1])
		if strings.Contains(name, " -> ") {
			continue
		}

		b := BranchInfo{
			Current:  strings.TrimSpace(parts[0]) == "*",
			Name:     name,
			IsRemote: strings.HasPrefix(name, "remotes/"),
		}
		if b.IsRemote {
			b.Name = strings.TrimPrefix(b.Name, "remotes/")
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
		if len(parts) > 6 {
			track := strings.TrimSpace(parts[6])
			if track != "" {
				fmt.Sscanf(track, "ahead %d", &b.Ahead)
				fmt.Sscanf(track, "behind %d", &b.Behind)
				if strings.Contains(track, ",") {
					p := strings.SplitN(track, ",", 2)
					fmt.Sscanf(strings.TrimSpace(p[0]), "ahead %d", &b.Ahead)
					fmt.Sscanf(strings.TrimSpace(p[1]), "behind %d", &b.Behind)
				}
			}
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

	out = strings.ReplaceAll(out, "\r\n", "\n")
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

func GetAheadBehind(repoPath string) (int, int) {
	out, err := gitCmd(
		repoPath,
		"rev-list",
		"--left-right",
		"--count",
		"HEAD...@{upstream}",
	)
	if err != nil {
		// fatal: no upstream configured for branch '...'
		if strings.Contains(err.Error(), "no upstream") {
			return -1, -1
		}
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
		"--format=§%H§%h§%s§%an§%ae§%aI§%ar§%D§%P",
	)
	if err != nil {
		return nil
	}

	var commits []Commit

	out = strings.ReplaceAll(out, "\r\n", "\n")
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.SplitN(line, "§", 10)
		if len(parts) < 10 {
			commits = append(commits, Commit{
				Graph:    line,
				IsCommit: false,
			})
			continue
		}

		graph := parts[0]
		t, _ := time.Parse(time.RFC3339, parts[6])

		c := Commit{
			Hash:        parts[1],
			ShortHash:   parts[2],
			Subject:     parts[3],
			Author:      parts[4],
			AuthorEmail: parts[5],
			Date:        t,
			DateRel:     parts[7],
			RefNames:    parts[8],
			Graph:       graph,
			IsCommit:    true,
		}

		if pStr := strings.TrimSpace(parts[9]); pStr != "" {
			c.Parents = strings.Split(pStr, " ")
		}

		commits = append(commits, c)
	}

	return commits
}

func GetStatus(repoPath string) []FileStatus {
	out, err := gitCmd(repoPath, "status", "--porcelain=v1")
	if err != nil {
		return nil
	}

	var files []FileStatus
	out = strings.ReplaceAll(out, "\r\n", "\n")
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}

		x := string(line[0])
		y := string(line[1])
		path := strings.TrimSpace(line[2:])
		
		if x != " " && x != "?" {
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

func GetStashes(repoPath string) []StashEntry {
	out, err := gitCmd(repoPath, "stash", "list", "--format=%gd|%s|%ar")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}

	var stashes []StashEntry
	out = strings.ReplaceAll(out, "\r\n", "\n")
	for i, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		s := StashEntry{Index: i}
		if len(parts) > 0 {
			s.Ref = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			s.Message = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			s.Date = strings.TrimSpace(parts[2])
		}
		stashes = append(stashes, s)
	}
	return stashes
}

func StashSave(repoPath, message string) error {
	return StashSaveExt(repoPath, message, false)
}

func StashSaveExt(repoPath, message string, includeUntracked bool) error {
	args := []string{"stash", "push"}
	if includeUntracked {
		args = append(args, "-u")
	}
	if message != "" {
		args = append(args, "-m", message)
	}
	_, err := gitCmd(repoPath, args...)
	return err
}

func StashPop(repoPath string, index int) error {
	_, err := gitCmd(repoPath, "stash", "pop", fmt.Sprintf("stash@{%d}", index))
	return err
}

func StashApply(repoPath string, index int) error {
	_, err := gitCmd(repoPath, "stash", "apply", fmt.Sprintf("stash@{%d}", index))
	return err
}

func StashDrop(repoPath string, index int) error {
	_, err := gitCmd(repoPath, "stash", "drop", fmt.Sprintf("stash@{%d}", index))
	return err
}

func StashShow(repoPath string, index int) string {
	out, err := gitCmd(repoPath, "stash", "show", "-p", "--no-color", fmt.Sprintf("stash@{%d}", index))
	if err != nil {
		return ""
	}
	return out
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
	out, err := gitCmd(repoPath, "show", "--no-color", "--patch", "--format=", hash)
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

func StageAll(repoPath string) error {
	_, err := gitCmd(repoPath, "add", "-A")
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

func CheckoutNewBranch(repoPath, branch string) error {
	_, err := gitCmd(repoPath, "checkout", "-b", branch)
	return err
}

func DeleteBranch(repoPath, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := gitCmd(repoPath, "branch", flag, branch)
	return err
}

func RenameBranch(repoPath, oldName, newName string) error {
	_, err := gitCmd(repoPath, "branch", "-m", oldName, newName)
	return err
}

func MergeBranch(repoPath, branch string) error {
	_, err := gitCmd(repoPath, "merge", branch, "--no-edit")
	return err
}

func RebaseBranch(repoPath, onto string) error {
	_, err := gitCmd(repoPath, "rebase", onto)
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

func PushSetUpstream(repoPath, remote, branch string) error {
	_, err := gitCmd(repoPath, "push", "--set-upstream", remote, branch)
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
		Stashes:  GetStashes(root),
		Ahead:    ahead,
		Behind:   behind,
	}
}

func DiscoverRepos(inputs []string, scanParent bool, starredPaths []string, starredOnly bool) []RepoInfo {
	seen := map[string]bool{}
	var repos []RepoInfo

	addRepo := func(path string, starred bool) {
		root, err := FindRepoRoot(path)
		if err != nil {
			return
		}
		if seen[root] {
			return
		}
		seen[root] = true
		repos = append(repos, RepoInfo{
			Name:    filepath.Base(root),
			Path:    root,
			Starred: starred,
		})
	}

	starredMap := make(map[string]bool)
	for _, p := range starredPaths {
		if IsGitRepo(p) {
			addRepo(p, true)
			starredMap[p] = true
		}
	}

	if !starredOnly {
		for _, p := range inputs {
			if p == "" {
				continue
			}

			if IsGitRepo(p) {
				addRepo(p, starredMap[p])
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
					addRepo(child, starredMap[child])
				}
			}
		}
	}

	sort.Slice(repos, func(i, j int) bool {
		if repos[i].Starred != repos[j].Starred {
			return repos[i].Starred
		}
		return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name)
	})

	return repos
}

func RevertFile(repoPath, filePath string) error {
	_, err := gitCmd(repoPath, "checkout", "HEAD", "--", filePath)
	return err
}

func StashSingleFile(repoPath, filePath string) error {
	_, err := gitCmd(repoPath, "stash", "push", "-m", "Stash file: "+filePath, "--", filePath)
	return err
}

func IgnoreFile(repoPath, filePath string) error {
	f, err := os.OpenFile(filepath.Join(repoPath, ".gitignore"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("\n" + filePath + "\n")
	return err
}

func GetIgnoredFiles(repoPath string) []string {
	out, err := gitCmd(repoPath, "ls-files", "--others", "--ignored", "--exclude-standard")
	if err != nil {
		return nil
	}
	lines := strings.Split(out, "\n")
	var cleaned []string
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			cleaned = append(cleaned, t)
		}
	}
	return cleaned
}

func UnignoreFile(repoPath, filePath string) error {
	ignorePath := filepath.Join(repoPath, ".gitignore")
	input, err := os.ReadFile(ignorePath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(input), "\n")
	var newLines []string
	for _, line := range lines {
		if strings.TrimSpace(line) != filePath {
			newLines = append(newLines, line)
		}
	}
	return os.WriteFile(ignorePath, []byte(strings.Join(newLines, "\n")), 0644)
}

func AmendCommit(repoPath, newMessage string) error {
	_, err := gitCmd(repoPath, "commit", "--amend", "-m", newMessage)
	return err
}

func OpenInEditor(repoPath, filePath, editorCmd string) {
	if editorCmd == "" {
		editorCmd = os.Getenv("EDITOR")
	}
	if editorCmd == "" {
		if runtime.GOOS == "windows" {
			editorCmd = "notepad"
		} else {
			editorCmd = "vi"
		}
	}

	fullPath := filepath.Join(repoPath, filePath)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", fmt.Sprintf("%s %s", editorCmd, fullPath))
	} else {
		cmd = exec.Command("sh", "-c", fmt.Sprintf("%s %s", editorCmd, fullPath))
	}
	cmd.Start()
}

func OpenInDiffTool(repoPath, filePath string) {
	cmd := exec.Command("git", "-C", repoPath, "difftool", "-y", filePath)
	cmd.Start()
}

func UnstageAll(repoPath string) error {
	_, err := gitCmd(repoPath, "reset")
	return err
}

func RevertAllUnstaged(repoPath string) error {
	_, err := gitCmd(repoPath, "checkout", "--", ".")
	return err
}

func PushForce(repoPath string) error {
	_, err := gitCmd(repoPath, "push", "--force-with-lease")
	return err
}

func Sync(repoPath string) error {
	if _, err := gitCmd(repoPath, "fetch", "--all"); err != nil {
		return err
	}
	_, err := gitCmd(repoPath, "pull")
	return err
}
func CheckoutCommit(repoPath, hash string) error {
	_, err := gitCmd(repoPath, "checkout", hash)
	return err
}

func CherryPickCommit(repoPath, hash string) error {
	_, err := gitCmd(repoPath, "cherry-pick", hash)
	return err
}

func ResetCommit(repoPath, hash string, hard bool) error {
	mode := "--soft"
	if hard {
		mode = "--hard"
	}
	_, err := gitCmd(repoPath, "reset", mode, hash)
	return err
}

func RebaseCommit(repoPath, hash string) error {
	_, err := gitCmd(repoPath, "rebase", hash)
	return err
}

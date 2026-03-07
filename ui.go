package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)
type App struct {
	cfg Config

	app *gtk.Application
	win *gtk.ApplicationWindow

	repos []RepoInfo
	state *RepoState

	repoListBox   *gtk.ListBox
	fileListBox   *gtk.ListBox
	commitListBox *gtk.ListBox
	diffView      *gtk.TextView
	diffBuf       *gtk.TextBuffer

	searchEntry  *gtk.SearchEntry
	branchDrop   *gtk.DropDown
	commitEntry  *gtk.Entry
	commitButton *gtk.Button

	repoTitle   *gtk.Label
	repoPath    *gtk.Label
	branchLabel *gtk.Label
	statsLabel  *gtk.Label
	aheadLabel  *gtk.Label
	emptyLabel  *gtk.Label
	stack       *gtk.Stack

	selectedRepoRow   *gtk.ListBoxRow
	selectedFileRow   *gtk.ListBoxRow
	selectedCommitRow *gtk.ListBoxRow

	selectedRepoPath string
	selectedFile     string
	selectedFileMode bool
	selectedCommit   string
}

func NewApp(cfg Config) *App {
	app := &App{cfg: cfg}
	app.repos = DiscoverRepos(
		cfg.Repos.Paths,
		cfg.Behavior.ScanParentOnStart,
	)
	return app
}

func (a *App) Run() {
	a.app = gtk.NewApplication("dev.timuzkas.gig", gio.ApplicationFlagsNone)
	a.app.ConnectActivate(func() {
		a.build()
	})
	a.app.Run(os.Args)
}

func (a *App) build() {
	a.loadCSS()

	a.win = gtk.NewApplicationWindow(a.app)
	a.win.SetTitle("gig")
	a.win.SetDefaultSize(1440, 900)

	root := gtk.NewBox(gtk.OrientationVertical, 0)

	header := a.buildHeader()
	root.Append(header)

	body := a.buildBody()
	body.SetVExpand(true)
	root.Append(body)

	commitStrip := a.buildCommitStrip()
	root.Append(commitStrip)

	statusStrip := a.buildStatusStrip()
	root.Append(statusStrip)

	a.win.SetChild(root)
	a.win.Present()

	if len(a.repos) > 0 {
		a.selectRepo(a.repos[0].Path)
	}

	if a.cfg.Behavior.AutoRefresh {
		glib.TimeoutAdd(
			uint(a.cfg.Behavior.RefreshIntervalSec*1000),
			func() bool {
				if a.state != nil {
					a.reloadState(false)
				}
				return true
			},
		)
	}
}

func (a *App) loadCSS() {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}

	provider := gtk.NewCSSProvider()

	cssVars := fmt.Sprintf(`
@define-color bg %s;
@define-color surface %s;
@define-color surface2 %s;
@define-color border %s;
@define-color text %s;
@define-color text_dim %s;
@define-color accent %s;
@define-color added %s;
@define-color removed %s;
@define-color modified %s;
@define-color selection %s;

* {
  font-family: %s;
  font-size: %dpt;
}
`,
		a.cfg.Colors.Bg,
		a.cfg.Colors.Surface,
		a.cfg.Colors.Surface2,
		a.cfg.Colors.Border,
		a.cfg.Colors.Text,
		a.cfg.Colors.TextDim,
		a.cfg.Colors.Accent,
		a.cfg.Colors.Added,
		a.cfg.Colors.Removed,
		a.cfg.Colors.Modified,
		a.cfg.Colors.Selection,
		a.cfg.Appearance.FontFamily,
		a.cfg.Appearance.FontSize,
	)

	style, err := os.ReadFile("style.css")
	if err == nil {
		cssVars += string(style)
	}

	provider.LoadFromData(cssVars)
	gtk.StyleContextAddProviderForDisplay(
		display,
		provider,
		gtk.STYLE_PROVIDER_PRIORITY_APPLICATION,
	)
}

func (a *App) buildHeader() *gtk.HeaderBar {
	header := gtk.NewHeaderBar()
	header.SetShowTitleButtons(true)

	left := gtk.NewBox(gtk.OrientationHorizontal, 8)

	a.branchDrop = gtk.NewDropDown(nil, nil)
	a.branchDrop.SetSizeRequest(220, -1)
	left.Append(a.branchDrop)

	fetchBtn := gtk.NewButtonWithLabel("Fetch")
	fetchBtn.ConnectClicked(func() {
		if a.state == nil {
			return
		}
		go func(repo string) {
			_ = Fetch(repo)
			glib.IdleAdd(func() {
				a.reloadState(true)
			})
		}(a.state.Path)
	})
	left.Append(fetchBtn)

	pullBtn := gtk.NewButtonWithLabel("Pull")
	pullBtn.ConnectClicked(func() {
		if a.state == nil {
			return
		}
		go func(repo string) {
			_ = Pull(repo)
			glib.IdleAdd(func() {
				a.reloadState(true)
			})
		}(a.state.Path)
	})
	left.Append(pullBtn)

	pushBtn := gtk.NewButtonWithLabel("Push")
	pushBtn.ConnectClicked(func() {
		if a.state == nil {
			return
		}
		go func(repo string) {
			_ = Push(repo)
			glib.IdleAdd(func() {
				a.reloadState(true)
			})
		}(a.state.Path)
	})
	left.Append(pushBtn)

	header.PackStart(left)

	right := gtk.NewBox(gtk.OrientationHorizontal, 8)

	a.searchEntry = gtk.NewSearchEntry()
	a.searchEntry.SetPlaceholderText("Search commits")
	a.searchEntry.SetWidthChars(28)
	a.searchEntry.ConnectSearchChanged(func() {
		a.populateCommits()
	})
	right.Append(a.searchEntry)

	refreshBtn := gtk.NewButtonWithLabel("Refresh")
	refreshBtn.ConnectClicked(func() {
		a.reloadState(true)
	})
	right.Append(refreshBtn)

	header.PackEnd(right)

	return header
}

func (a *App) buildBody() *gtk.Paned {
	mainPaned := gtk.NewPaned(gtk.OrientationHorizontal)

	repoSidebar := a.buildRepoSidebar()
	repoSidebar.SetSizeRequest(a.cfg.Appearance.RepoSidebarWidth, -1)
	mainPaned.SetStartChild(repoSidebar)

	rightPaned := gtk.NewPaned(gtk.OrientationHorizontal)

	fileSidebar := a.buildFileSidebar()
	fileSidebar.SetSizeRequest(a.cfg.Appearance.SidebarWidth, -1)
	rightPaned.SetStartChild(fileSidebar)

	content := a.buildContentArea()
	rightPaned.SetEndChild(content)

	mainPaned.SetEndChild(rightPaned)

	mainPaned.SetResizeStartChild(false)
	mainPaned.SetShrinkStartChild(false)
	rightPaned.SetResizeStartChild(false)
	rightPaned.SetShrinkStartChild(false)

	return mainPaned
}

func (a *App) buildRepoSidebar() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("repo-sidebar")

	titleWrap := gtk.NewBox(gtk.OrientationVertical, 4)
	titleWrap.SetMarginTop(12)
	titleWrap.SetMarginBottom(8)
	titleWrap.SetMarginStart(12)
	titleWrap.SetMarginEnd(12)

	title := gtk.NewLabel("REPOSITORIES")
	title.SetXAlign(0)
	title.AddCSSClass("section-title")
	titleWrap.Append(title)

	box.Append(titleWrap)

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)

	a.repoListBox = gtk.NewListBox()
	a.repoListBox.SetSelectionMode(gtk.SelectionNone)
	scroll.SetChild(a.repoListBox)

	box.Append(scroll)
	a.populateRepos()

	return box
}

func (a *App) buildFileSidebar() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("file-sidebar")

	top := gtk.NewBox(gtk.OrientationVertical, 4)
	top.SetMarginTop(12)
	top.SetMarginBottom(8)
	top.SetMarginStart(12)
	top.SetMarginEnd(12)

	title := gtk.NewLabel("CHANGES")
	title.SetXAlign(0)
	title.AddCSSClass("section-title")
	top.Append(title)

	box.Append(top)

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)

	a.fileListBox = gtk.NewListBox()
	a.fileListBox.SetSelectionMode(gtk.SelectionNone)

	scroll.SetChild(a.fileListBox)
	box.Append(scroll)

	return box
}

func (a *App) buildContentArea() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("content-panel")

	topInfo := gtk.NewBox(gtk.OrientationVertical, 2)
	topInfo.SetMarginTop(12)
	topInfo.SetMarginBottom(12)
	topInfo.SetMarginStart(14)
	topInfo.SetMarginEnd(14)

	a.repoTitle = gtk.NewLabel("")
	a.repoTitle.SetXAlign(0)
	a.repoTitle.AddCSSClass("repo-name")

	a.repoPath = gtk.NewLabel("")
	a.repoPath.SetXAlign(0)
	a.repoPath.AddCSSClass("repo-path")
	a.repoPath.SetEllipsize(3)

	topInfo.Append(a.repoTitle)
	topInfo.Append(a.repoPath)

	box.Append(topInfo)

	switcher := gtk.NewStackSwitcher()
	box.Append(switcher)

	a.stack = gtk.NewStack()
	a.stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	a.stack.SetTransitionDuration(160)
	a.stack.SetVExpand(true)
	a.stack.SetHExpand(true)
	switcher.SetStack(a.stack)

	logAndDiff := a.buildLogAndDiff()
	a.stack.AddTitled(logAndDiff, "history", "History")

	emptyWrap := gtk.NewBox(gtk.OrientationVertical, 0)
	emptyWrap.SetVExpand(true)
	emptyWrap.SetHExpand(true)
	emptyWrap.SetVAlign(gtk.AlignCenter)
	emptyWrap.SetHAlign(gtk.AlignCenter)

	a.emptyLabel = gtk.NewLabel("Select a repository")
	a.emptyLabel.AddCSSClass("empty")
	emptyWrap.Append(a.emptyLabel)

	a.stack.AddTitled(emptyWrap, "empty", "Empty")
	a.stack.SetVisibleChildName("empty")

	box.Append(a.stack)

	return box
}

func (a *App) buildLogAndDiff() *gtk.Paned {
	paned := gtk.NewPaned(gtk.OrientationVertical)

	logScroll := gtk.NewScrolledWindow()
	logScroll.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	logScroll.SetVExpand(true)

	a.commitListBox = gtk.NewListBox()
	a.commitListBox.SetSelectionMode(gtk.SelectionNone)
	logScroll.SetChild(a.commitListBox)

	diffScroll := gtk.NewScrolledWindow()
	diffScroll.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	diffScroll.SetVExpand(true)

	a.diffBuf = gtk.NewTextBuffer(nil)
	a.diffView = gtk.NewTextViewWithBuffer(a.diffBuf)
	a.diffView.SetEditable(false)
	a.diffView.SetCursorVisible(false)
	a.diffView.SetMonospace(true)
	a.diffView.SetTopMargin(10)
	a.diffView.SetBottomMargin(10)
	a.diffView.SetLeftMargin(14)
	a.diffView.SetRightMargin(14)
	a.diffView.AddCSSClass("diff-view")

	diffScroll.SetChild(a.diffView)

	paned.SetStartChild(logScroll)
	paned.SetEndChild(diffScroll)
	paned.SetPosition(320)

	return paned
}

func (a *App) buildCommitStrip() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.AddCSSClass("commit-strip")

	a.commitEntry = gtk.NewEntry()
	a.commitEntry.SetPlaceholderText("Commit message")
	a.commitEntry.SetHExpand(true)
	a.commitEntry.ConnectChanged(func() {
		a.updateCommitButton()
	})
	box.Append(a.commitEntry)

	a.commitButton = gtk.NewButtonWithLabel("Commit")
	a.commitButton.AddCSSClass("suggested-action")
	a.commitButton.SetSensitive(false)
	a.commitButton.ConnectClicked(func() {
		if a.state == nil {
			return
		}

		msg := strings.TrimSpace(a.commitEntry.Text())
		if msg == "" {
			return
		}

		go func(repo, message string) {
			_ = DoCommit(repo, message)
			glib.IdleAdd(func() {
				a.commitEntry.SetText("")
				a.reloadState(true)
			})
		}(a.state.Path, msg)
	})
	box.Append(a.commitButton)

	return box
}

func (a *App) buildStatusStrip() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 12)
	box.AddCSSClass("status-strip")

	a.branchLabel = gtk.NewLabel("")
	a.branchLabel.SetXAlign(0)

	a.aheadLabel = gtk.NewLabel("")
	a.aheadLabel.SetXAlign(0)

	a.statsLabel = gtk.NewLabel("")
	a.statsLabel.SetXAlign(0)

	box.Append(a.branchLabel)
	box.Append(a.aheadLabel)
	box.Append(a.statsLabel)

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	box.Append(spacer)

	return box
}

func (a *App) populateRepos() {
	clearListBox(a.repoListBox)

	if len(a.repos) == 0 {
		row := gtk.NewListBoxRow()
		row.SetSelectable(false)

		label := gtk.NewLabel("No repositories found")
		label.SetXAlign(0)
		label.SetMarginTop(12)
		label.SetMarginBottom(12)
		label.SetMarginStart(12)
		row.SetChild(label)

		a.repoListBox.Append(row)
		return
	}

	for _, repo := range a.repos {
		repo := repo

		row := gtk.NewListBoxRow()
		row.AddCSSClass("repo-row")

		box := gtk.NewBox(gtk.OrientationVertical, 2)
		box.AddCSSClass("repo-row-box")

		name := gtk.NewLabel(repo.Name)
		name.SetXAlign(0)
		name.AddCSSClass("repo-name")

		path := gtk.NewLabel(repo.Path)
		path.SetXAlign(0)
		path.SetEllipsize(3)
		path.AddCSSClass("repo-path")

		box.Append(name)
		box.Append(path)
		row.SetChild(box)

		click := gtk.NewGestureClick()
		click.ConnectReleased(func(_ int, _, _ float64) {
			a.selectRepo(repo.Path)
		})
		row.AddController(click)

		a.repoListBox.Append(row)
	}
}

func (a *App) selectRepo(path string) {
	a.selectedRepoPath = path
	a.selectedFile = ""
	a.selectedCommit = ""

	
	idx := 0
	for i, repo := range a.repos {
		if repo.Path == path {
			idx = i
			break
		}
	}

	row := a.repoListBox.RowAtIndex(idx)
	if row != nil {
		if a.selectedRepoRow != nil {
			a.selectedRepoRow.RemoveCSSClass("selected")
		}
		row.AddCSSClass("selected")
		a.selectedRepoRow = row
	}

	a.reloadState(true)
}

func (a *App) reloadState(showDefaultDiff bool) {
	if a.selectedRepoPath == "" {
		return
	}

	newState := LoadRepoState(a.selectedRepoPath, a.cfg.Behavior.MaxCommits)
	if newState == nil {
		return
	}

	a.state = newState
	a.bindBranchDrop()
	a.populateFiles()
	a.populateCommits()
	a.updateHeaderInfo()
	a.updateStatusStrip()
	a.stack.SetVisibleChildName("history")

	if showDefaultDiff {
		if len(a.state.Files) > 0 {
			f := a.state.Files[0]
			a.selectedFile = f.Path
			a.selectedFileMode = f.Staged
			a.selectedCommit = ""
			a.renderDiff(GetFileDiff(a.state.Path, f.Path, f.Staged))
		} else if len(a.state.Commits) > 0 {
			c := a.state.Commits[0]
			a.selectedCommit = c.Hash
			a.selectedFile = ""
			a.renderDiff(GetCommitDiff(a.state.Path, c.Hash))
		} else {
			a.renderDiff("")
		}
	}

	a.updateCommitButton()
}

func (a *App) bindBranchDrop() {
	if a.state == nil {
		return
	}

	branches := make([]string, 0, len(a.state.Branches))
	currentIndex := uint(0)

	for i, b := range a.state.Branches {
		branches = append(branches, b.Name)
		if b.Current {
			currentIndex = uint(i)
		}
	}

	model := gtk.NewStringList(branches)
	a.branchDrop.SetModel(model)
	a.branchDrop.SetSelected(currentIndex)

	a.branchDrop.Connect("notify::selected", func() {
		if a.state == nil {
			return
		}

		sel := int(a.branchDrop.Selected())
		if sel < 0 || sel >= len(branches) {
			return
		}

		target := branches[sel]
		if target == a.state.Branch {
			return
		}

		go func(repo, branch string) {
			_ = Checkout(repo, branch)
			glib.IdleAdd(func() {
				a.reloadState(true)
			})
		}(a.state.Path, target)
	})
}

func (a *App) populateFiles() {
	clearListBox(a.fileListBox)
	a.selectedFileRow = nil
	
	if a.state == nil || len(a.state.Files) == 0 {
		row := gtk.NewListBoxRow()
		row.SetSelectable(false)

		label := gtk.NewLabel("Working tree clean")
		label.SetXAlign(0)
		label.SetMarginTop(12)
		label.SetMarginBottom(12)
		label.SetMarginStart(12)
		row.SetChild(label)

		a.fileListBox.Append(row)
		return
	}

	staged := []FileStatus{}
	unstaged := []FileStatus{}

	for _, f := range a.state.Files {
		if f.Staged {
			staged = append(staged, f)
		} else {
			unstaged = append(unstaged, f)
		}
	}

	if len(staged) > 0 {
		a.appendFileSection("STAGED", staged, true)
	}
	if len(unstaged) > 0 {
		a.appendFileSection("CHANGES", unstaged, false)
	}
}

func (a *App) appendFileSection(title string, files []FileStatus, staged bool) {
	headerRow := gtk.NewListBoxRow()
	headerRow.SetSelectable(false)

	label := gtk.NewLabel(fmt.Sprintf("%s  (%d)", title, len(files)))
	label.SetXAlign(0)
	label.SetMarginTop(10)
	label.SetMarginBottom(6)
	label.SetMarginStart(12)
	label.AddCSSClass("section-title")

	headerRow.SetChild(label)
	a.fileListBox.Append(headerRow)

	for _, f := range files {
		f := f

		row := gtk.NewListBoxRow()
		row.AddCSSClass("file-row")

		box := gtk.NewBox(gtk.OrientationHorizontal, 8)
		box.AddCSSClass("file-row-box")

		status := gtk.NewLabel(statusMark(f))
		status.SetWidthChars(2)
		status.SetXAlign(0)

		switch statusKind(f) {
		case "A":
			status.AddCSSClass("status-added")
		case "M":
			status.AddCSSClass("status-modified")
		case "D":
			status.AddCSSClass("status-removed")
		default:
			status.AddCSSClass("status-unknown")
		}

		name := gtk.NewLabel(f.Path)
		name.SetHExpand(true)
		name.SetXAlign(0)
		name.SetEllipsize(3)

		actionLabel := "+"
		if staged {
			actionLabel = "−"
		}

		action := gtk.NewButtonWithLabel(actionLabel)
		action.AddCSSClass("flat")
		action.SetSizeRequest(28, 28)
		action.ConnectClicked(func() {
			if a.state == nil {
				return
			}
			go func(repo string, file string, isStaged bool) {
				if isStaged {
					_ = UnstageFile(repo, file)
				} else {
					_ = StageFile(repo, file)
				}
				glib.IdleAdd(func() {
					a.reloadState(false)
					a.selectedFile = file
					a.selectedFileMode = !isStaged
					a.selectedCommit = ""
					if a.state != nil {
						a.renderDiff(GetFileDiff(a.state.Path, file, !isStaged))
					}
				})
			}(a.state.Path, f.Path, staged)
		})

		box.Append(status)
		box.Append(name)
		box.Append(action)
		row.SetChild(box)

		click := gtk.NewGestureClick()
		click.ConnectReleased(func(_ int, _, _ float64) {
			a.selectedFile = f.Path
			a.selectedFileMode = staged
			a.selectedCommit = ""
			a.renderDiff(GetFileDiff(a.state.Path, f.Path, staged))
			a.markSelectedRow(a.fileListBox, row)
		})
		row.AddController(click)

		a.fileListBox.Append(row)
	}
}

func (a *App) populateCommits() {
	clearListBox(a.commitListBox)
	a.selectedCommitRow = nil

	if a.state == nil {
		return
	}

	filter := strings.ToLower(strings.TrimSpace(a.searchEntry.Text()))
	count := 0

	for _, c := range a.state.Commits {
		if filter != "" {
			searchable := strings.ToLower(
				c.Subject + " " + c.Author + " " + c.ShortHash,
			)
			if !strings.Contains(searchable, filter) {
				continue
			}
		}

		c := c

		row := gtk.NewListBoxRow()
		row.AddCSSClass("commit-row")

		box := gtk.NewBox(gtk.OrientationHorizontal, 12)
		box.AddCSSClass("commit-row-box")

		hash := gtk.NewLabel(c.ShortHash)
		hash.SetXAlign(0)
		hash.SetWidthChars(8)
		hash.AddCSSClass("commit-hash")

		subjectWrap := gtk.NewBox(gtk.OrientationVertical, 2)
		subjectWrap.SetHExpand(true)

		subject := gtk.NewLabel(c.Subject)
		subject.SetXAlign(0)
		subject.SetEllipsize(3)

		meta := gtk.NewLabel(c.Author + " · " + c.DateRel)
		meta.SetXAlign(0)
		meta.AddCSSClass("commit-meta")

		subjectWrap.Append(subject)
		subjectWrap.Append(meta)

		box.Append(hash)
		box.Append(subjectWrap)
		row.SetChild(box)

		click := gtk.NewGestureClick()
		click.ConnectReleased(func(_ int, _, _ float64) {
			a.selectedCommit = c.Hash
			a.selectedFile = ""
			a.renderDiff(GetCommitDiff(a.state.Path, c.Hash))
			a.markSelectedRow(a.commitListBox, row)
		})
		row.AddController(click)

		a.commitListBox.Append(row)
		count++
	}

	if count == 0 {
		row := gtk.NewListBoxRow()
		row.SetSelectable(false)

		label := gtk.NewLabel("No commits match your search")
		label.SetXAlign(0)
		label.SetMarginTop(12)
		label.SetMarginBottom(12)
		label.SetMarginStart(12)
		row.SetChild(label)

		a.commitListBox.Append(row)
	}
}

func (a *App) updateHeaderInfo() {
	if a.state == nil {
		a.repoTitle.SetText("")
		a.repoPath.SetText("")
		return
	}

	a.repoTitle.SetText(a.state.Name)
	a.repoPath.SetText(a.state.Path)
}

func (a *App) updateStatusStrip() {
	if a.state == nil {
		a.branchLabel.SetText("")
		a.aheadLabel.SetText("")
		a.statsLabel.SetText("")
		return
	}

	staged := 0
	changed := 0

	for _, f := range a.state.Files {
		if f.Staged {
			staged++
		} else {
			changed++
		}
	}

	a.branchLabel.SetText("Branch: " + a.state.Branch)

	if a.state.Ahead > 0 || a.state.Behind > 0 {
		a.aheadLabel.SetText(
			fmt.Sprintf("↑%d ↓%d", a.state.Ahead, a.state.Behind),
		)
	} else {
		a.aheadLabel.SetText("")
	}

	a.statsLabel.SetText(fmt.Sprintf("%d staged · %d changed", staged, changed))
}

func (a *App) updateCommitButton() {
	if a.state == nil {
		a.commitButton.SetSensitive(false)
		return
	}

	msg := strings.TrimSpace(a.commitEntry.Text())
	hasStaged := false

	for _, f := range a.state.Files {
		if f.Staged {
			hasStaged = true
			break
		}
	}

	a.commitButton.SetSensitive(msg != "" && hasStaged)
}

func (a *App) renderDiff(diff string) {
	a.diffBuf.SetText("")

	if strings.TrimSpace(diff) == "" {
		a.diffBuf.SetText("No changes to display")
		return
	}

	tagTable := a.diffBuf.TagTable()

	ensureTag := func(name, fg string) {
		if tagTable.Lookup(name) != nil {
			return
		}
		tag := gtk.NewTextTag(name)
		tagTable.Add(tag)
	}
	
	ensureTag("added", a.cfg.Colors.Added)
	ensureTag("removed", a.cfg.Colors.Removed)
	ensureTag("header", a.cfg.Colors.Accent)
	ensureTag("hunk", a.cfg.Colors.TextDim)
	ensureTag("normal", a.cfg.Colors.Text)

	for _, line := range strings.Split(diff, "\n") {
		start := a.diffBuf.EndIter()
		a.diffBuf.Insert(start, line+"\n")
		end := a.diffBuf.EndIter()
		

		tag := "normal"
		switch {
		case strings.HasPrefix(line, "diff "):
			tag = "header"
		case strings.HasPrefix(line, "index "):
			tag = "header"
		case strings.HasPrefix(line, "+++ "):
			tag = "header"
		case strings.HasPrefix(line, "--- "):
			tag = "header"
		case strings.HasPrefix(line, "@@"):
			tag = "hunk"
		case strings.HasPrefix(line, "+"):
			tag = "added"
		case strings.HasPrefix(line, "-"):
			tag = "removed"
		}

		lineStart := *end
		lineStart.BackwardChars(len([]rune(line + "\n")))
		a.diffBuf.ApplyTagByName(tag, &lineStart, end)
	}
}

func (a *App) markSelectedRow(list *gtk.ListBox, selected *gtk.ListBoxRow) {
	if list == a.fileListBox {
		if a.selectedFileRow != nil {
			a.selectedFileRow.RemoveCSSClass("selected")
		}
		selected.AddCSSClass("selected")
		a.selectedFileRow = selected
		return
	}

	if list == a.commitListBox {
		if a.selectedCommitRow != nil {
			a.selectedCommitRow.RemoveCSSClass("selected")
		}
		selected.AddCSSClass("selected")
		a.selectedCommitRow = selected
	}
}

func clearListBox(list *gtk.ListBox) {
	for {
		row := list.RowAtIndex(0)
		if row == nil {
			break
		}
		list.Remove(row)
	}
}


func statusMark(f FileStatus) string {
	k := statusKind(f)
	switch k {
	case "A":
		return "A"
	case "M":
		return "M"
	case "D":
		return "D"
	case "?":
		return "U"
	default:
		return "•"
	}
}

func statusKind(f FileStatus) string {
	s := f.WorkStatus
	if f.Staged {
		s = f.IndexStatus
	}
	if s == "" || s == " " {
		return "?"
	}
	return s
}

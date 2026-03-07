package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// ── App struct ────────────────────────────────────────────────────

type App struct {
	cfg   Config
	app   *gtk.Application
	win   *gtk.ApplicationWindow

	repos []RepoInfo
	state *RepoState

	// Layout
	rootOverlay   *gtk.Overlay
	overlayBox    *gtk.Box
	overlayReveal *gtk.Revealer

	// Lists
	repoListBox   *gtk.ListBox
	fileListBox   *gtk.ListBox
	commitListBox *gtk.ListBox
	branchListBox *gtk.ListBox
	stashListBox  *gtk.ListBox

	// Diff
	diffView *gtk.TextView
	diffBuf  *gtk.TextBuffer

	// Controls
	searchEntry  *gtk.SearchEntry
	branchSearch *gtk.SearchEntry
	branchDrop   *gtk.DropDown
	commitEntry  *gtk.Entry
	commitButton *gtk.Button

	// Labels
	repoTitle   *gtk.Label
	repoPath    *gtk.Label
	infoLabel   *gtk.Label
	remoteLabel *gtk.Label
	branchLabel *gtk.Label
	aheadLabel  *gtk.Label
	statsLabel  *gtk.Label
	emptyLabel  *gtk.Label

	stack *gtk.Stack

	// Selection tracking
	selectedRepoRow   *gtk.ListBoxRow
	selectedFileRow   *gtk.ListBoxRow
	selectedCommitRow *gtk.ListBoxRow

	selectedRepoPath string
	selectedFile     string
	selectedFileMode bool
	selectedCommit   string

	branchDropBound bool
	diffReqID       uint64
}

func NewApp(cfg Config) *App {
	a := &App{cfg: cfg}
	a.repos = DiscoverRepos(cfg.Repos.Paths, cfg.Behavior.ScanParentOnStart)
	return a
}

func (a *App) Run() {
	a.app = gtk.NewApplication("dev.timuzkas.gig", gio.ApplicationFlagsNone)
	a.app.ConnectActivate(a.build)
	a.app.Run(os.Args)
}

// ── Build ─────────────────────────────────────────────────────────

func (a *App) build() {
	a.loadCSS()

	a.win = gtk.NewApplicationWindow(a.app)
	a.win.SetTitle("gig")
	a.win.SetDefaultSize(1440, 900)
	a.win.SetResizable(true)

	root := gtk.NewBox(gtk.OrientationVertical, 0)
	root.Append(a.buildHeader())

	a.rootOverlay = gtk.NewOverlay()
	body := a.buildBody()
	body.SetVExpand(true)
	body.SetHExpand(true)
	a.rootOverlay.SetChild(body)
	a.buildOverlayPanel()
	a.rootOverlay.AddOverlay(a.overlayBox)
	root.Append(a.rootOverlay)

	root.Append(a.buildCommitStrip())
	root.Append(a.buildStatusStrip())

	a.win.SetChild(root)
	a.win.Present()

	if len(a.repos) > 0 {
		a.selectRepo(a.repos[0].Path)
	}

	if a.cfg.Behavior.AutoRefresh {
		glib.TimeoutAdd(uint(a.cfg.Behavior.RefreshIntervalSec*1000), func() bool {
			if a.state != nil {
				a.doReload(false)
			}
			return true
		})
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

* { font-family: %s; font-size: %dpt; }
`,
		a.cfg.Colors.Bg, a.cfg.Colors.Surface, a.cfg.Colors.Surface2,
		a.cfg.Colors.Border, a.cfg.Colors.Text, a.cfg.Colors.TextDim,
		a.cfg.Colors.Accent, a.cfg.Colors.Added, a.cfg.Colors.Removed,
		a.cfg.Colors.Modified, a.cfg.Colors.Selection,
		a.cfg.Appearance.FontFamily, a.cfg.Appearance.FontSize,
	)
	if style, err := os.ReadFile("style.css"); err == nil {
		cssVars += string(style)
	}
	provider.LoadFromData(cssVars)
	gtk.StyleContextAddProviderForDisplay(display, provider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
}

// ── In-app overlay ────────────────────────────────────────────────

func (a *App) buildOverlayPanel() {
	a.overlayBox = gtk.NewBox(gtk.OrientationVertical, 0)
	a.overlayBox.SetVAlign(gtk.AlignCenter)
	a.overlayBox.SetHAlign(gtk.AlignCenter)
	a.overlayBox.SetVExpand(true)
	a.overlayBox.SetHExpand(true)
	a.overlayBox.SetVisible(false)

	a.overlayReveal = gtk.NewRevealer()
	a.overlayReveal.SetTransitionType(gtk.RevealerTransitionTypeSlideUp)
	a.overlayReveal.SetTransitionDuration(180)
	a.overlayReveal.SetRevealChild(false)
	a.overlayBox.Append(a.overlayReveal)
}

func (a *App) showOverlay(child gtk.Widgetter) {
	if prev := a.overlayReveal.Child(); prev != nil {
		a.overlayReveal.SetChild(nil)
	}
	a.overlayReveal.SetChild(child)
	a.overlayBox.SetVisible(true)
	glib.TimeoutAdd(16, func() bool {
		a.overlayReveal.SetRevealChild(true)
		return false
	})
}

func (a *App) hideOverlay() {
	a.overlayReveal.SetRevealChild(false)
	glib.TimeoutAdd(200, func() bool {
		a.overlayBox.SetVisible(false)
		if prev := a.overlayReveal.Child(); prev != nil {
			a.overlayReveal.SetChild(nil)
		}
		return false
	})
}

func (a *App) buildOverlayCard(title string, width int, content gtk.Widgetter, extraBtns ...gtk.Widgetter) *gtk.Box {
	card := gtk.NewBox(gtk.OrientationVertical, 0)
	card.AddCSSClass("overlay-panel")
	if width > 0 {
		card.SetSizeRequest(width, -1)
	}

	hdr := gtk.NewBox(gtk.OrientationHorizontal, 8)
	hdr.AddCSSClass("overlay-header")

	lbl := gtk.NewLabel(title)
	lbl.SetXAlign(0)
	lbl.SetHExpand(true)
	hdr.Append(lbl)
	for _, b := range extraBtns {
		hdr.Append(b)
	}

	closeBtn := gtk.NewButtonWithLabel("✕")
	closeBtn.AddCSSClass("flat")
	closeBtn.ConnectClicked(func() { a.hideOverlay() })
	hdr.Append(closeBtn)
	card.Append(hdr)

	wrap := gtk.NewBox(gtk.OrientationVertical, 0)
	wrap.SetVExpand(true)
	wrap.Append(content)
	card.Append(wrap)
	return card
}

// confirmDialog — replaces all gtk.Dialog destructive confirmations
func (a *App) confirmDialog(title, body string, destructive bool, onConfirm func()) {
	content := gtk.NewBox(gtk.OrientationVertical, 14)
	content.SetMarginTop(16)
	content.SetMarginBottom(16)
	content.SetMarginStart(16)
	content.SetMarginEnd(16)

	if body != "" {
		lbl := gtk.NewLabel(body)
		lbl.SetXAlign(0)
		lbl.SetWrap(true)
		lbl.AddCSSClass("dim")
		content.Append(lbl)
	}

	btnRow := gtk.NewBox(gtk.OrientationHorizontal, 8)
	btnRow.SetHAlign(gtk.AlignEnd)

	cancelBtn := gtk.NewButtonWithLabel("Cancel")
	cancelBtn.ConnectClicked(func() { a.hideOverlay() })
	btnRow.Append(cancelBtn)

	okBtn := gtk.NewButtonWithLabel("Confirm")
	if destructive {
		okBtn.AddCSSClass("destructive-action")
	} else {
		okBtn.AddCSSClass("suggested-action")
	}
	okBtn.ConnectClicked(func() {
		a.hideOverlay()
		onConfirm()
	})
	btnRow.Append(okBtn)
	content.Append(btnRow)

	card := a.buildOverlayCard(title, 380, content)
	a.showOverlay(card)
}

// promptDialog — single text input in-app
func (a *App) promptDialog(title, placeholder, initial string, onOK func(string)) {
	content := gtk.NewBox(gtk.OrientationVertical, 14)
	content.SetMarginTop(16)
	content.SetMarginBottom(16)
	content.SetMarginStart(16)
	content.SetMarginEnd(16)

	entry := gtk.NewEntry()
	entry.SetPlaceholderText(placeholder)
	entry.SetText(initial)
	content.Append(entry)

	btnRow := gtk.NewBox(gtk.OrientationHorizontal, 8)
	btnRow.SetHAlign(gtk.AlignEnd)

	cancelBtn := gtk.NewButtonWithLabel("Cancel")
	cancelBtn.ConnectClicked(func() { a.hideOverlay() })
	btnRow.Append(cancelBtn)

	okBtn := gtk.NewButtonWithLabel("OK")
	okBtn.AddCSSClass("suggested-action")
	okBtn.ConnectClicked(func() {
		val := strings.TrimSpace(entry.Text())
		a.hideOverlay()
		onOK(val)
	})
	btnRow.Append(okBtn)
	content.Append(btnRow)

	card := a.buildOverlayCard(title, 420, content)
	a.showOverlay(card)
}

// ── Header ────────────────────────────────────────────────────────

func (a *App) buildHeader() *gtk.HeaderBar {
	hdr := gtk.NewHeaderBar()
	hdr.SetShowTitleButtons(true)

	left := gtk.NewBox(gtk.OrientationHorizontal, 5)

	// Branch dropdown — distinct bg from headerbar
	a.branchDrop = gtk.NewDropDown(nil, nil)
	a.branchDrop.SetSizeRequest(190, -1)
	left.Append(a.branchDrop)

	for _, def := range []struct {
		label, tooltip string
		fn             func()
	}{
		{"↓ Fetch", "Fetch all remotes", func() { a.runGitOp("Fetching…", "Fetch complete", Fetch) }},
		{"⇓ Pull", "Pull current branch", func() { a.runGitOp("Pulling…", "Pull complete", Pull) }},
		{"⇑ Push", "Push current branch", func() { a.runGitOp("Pushing…", "Push complete", Push) }},
		{"⊟ Stash", "Manage stashes", func() { a.openStashPanel() }},
		{"⚙ Remotes", "Manage remotes", func() { a.openRepoConfigPanel() }},
	} {
		def := def
		btn := gtk.NewButtonWithLabel(def.label)
		btn.SetTooltipText(def.tooltip)
		btn.ConnectClicked(def.fn)
		left.Append(btn)
	}

	hdr.PackStart(left)

	right := gtk.NewBox(gtk.OrientationHorizontal, 5)

	a.searchEntry = gtk.NewSearchEntry()
	a.searchEntry.SetPlaceholderText("Search commits…")
	a.searchEntry.SetWidthChars(24)
	a.searchEntry.ConnectSearchChanged(func() { a.populateCommits() })
	right.Append(a.searchEntry)

	refreshBtn := gtk.NewButtonWithLabel("↺")
	refreshBtn.SetTooltipText("Refresh")
	refreshBtn.ConnectClicked(func() { a.doReload(true) })
	right.Append(refreshBtn)

	hdr.PackEnd(right)
	return hdr
}

// runGitOp is a helper for simple async git operations
func (a *App) runGitOp(startMsg, okMsg string, fn func(string) error) {
	if a.state == nil {
		return
	}
	a.setInfo(startMsg)
	go func(repo string) {
		err := fn(repo)
		glib.IdleAdd(func() {
			if err != nil {
				a.setInfoErr(strings.TrimPrefix(err.Error(), "exit status 1: "))
			} else {
				a.setInfoOk(okMsg)
			}
			a.doReload(true)
		})
	}(a.state.Path)
}

// ── Body layout ───────────────────────────────────────────────────

func (a *App) buildBody() *gtk.Paned {
	outer := gtk.NewPaned(gtk.OrientationHorizontal)

	repoSidebar := a.buildRepoSidebar()
	repoSidebar.SetSizeRequest(a.cfg.Appearance.RepoSidebarWidth, -1)
	outer.SetStartChild(repoSidebar)
	outer.SetResizeStartChild(false)
	outer.SetShrinkStartChild(false)

	inner := gtk.NewPaned(gtk.OrientationHorizontal)

	fileSidebar := a.buildFileSidebar()
	fileSidebar.SetSizeRequest(a.cfg.Appearance.SidebarWidth, -1)
	inner.SetStartChild(fileSidebar)
	inner.SetResizeStartChild(false)
	inner.SetShrinkStartChild(false)

	inner.SetEndChild(a.buildContentArea())
	outer.SetEndChild(inner)
	return outer
}

// ── Repo sidebar ──────────────────────────────────────────────────

func (a *App) buildRepoSidebar() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("repo-sidebar")

	hdr := a.makeSidebarHeader("Repositories", nil)
	box.Append(hdr)

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

// ── File sidebar ──────────────────────────────────────────────────

func (a *App) buildFileSidebar() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("file-sidebar")

	// Stage All button lives in the header area
	stageAllBtn := gtk.NewButtonWithLabel("+ All")
	stageAllBtn.AddCSSClass("flat")
	stageAllBtn.SetTooltipText("Stage all changes")
	stageAllBtn.ConnectClicked(func() {
		if a.state == nil {
			return
		}
		go func(repo string) {
			_ = StageAll(repo)
			glib.IdleAdd(func() { a.doReload(false) })
		}(a.state.Path)
	})

	hdr := a.makeSidebarHeader("Changes", stageAllBtn)
	box.Append(hdr)

	a.remoteLabel = gtk.NewLabel("")
	a.remoteLabel.SetXAlign(0)
	a.remoteLabel.AddCSSClass("repo-path")
	a.remoteLabel.SetMarginStart(14)
	a.remoteLabel.SetMarginBottom(4)
	box.Append(a.remoteLabel)

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)

	a.fileListBox = gtk.NewListBox()
	a.fileListBox.SetSelectionMode(gtk.SelectionNone)
	scroll.SetChild(a.fileListBox)
	box.Append(scroll)
	return box
}

// makeSidebarHeader creates a consistent sidebar section header with
// transparent bg so it inherits @surface from the parent panel.
func (a *App) makeSidebarHeader(title string, extra gtk.Widgetter) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.AddCSSClass("sidebar-section")
	box.SetMarginTop(11)
	box.SetMarginBottom(9)
	box.SetMarginStart(14)
	box.SetMarginEnd(10)

	lbl := gtk.NewLabel(title)
	lbl.SetXAlign(0)
	lbl.SetHExpand(true)
	lbl.AddCSSClass("section-label")
	box.Append(lbl)

	if extra != nil {
		box.Append(extra)
	}
	return box
}

// ── Content area ──────────────────────────────────────────────────

func (a *App) buildContentArea() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("content-panel")

	// Single-row compact info bar: [RepoName]  [path …]  [stats right-aligned]
	infoBar := gtk.NewBox(gtk.OrientationHorizontal, 10)
	infoBar.AddCSSClass("top-info-bar")
	infoBar.SetMarginTop(7)
	infoBar.SetMarginBottom(7)
	infoBar.SetMarginStart(14)
	infoBar.SetMarginEnd(14)

	a.repoTitle = gtk.NewLabel("")
	a.repoTitle.SetXAlign(0)
	a.repoTitle.AddCSSClass("repo-name")
	infoBar.Append(a.repoTitle)

	a.repoPath = gtk.NewLabel("")
	a.repoPath.SetXAlign(0)
	a.repoPath.SetHExpand(true)
	a.repoPath.AddCSSClass("repo-path")
	a.repoPath.SetEllipsize(3)
	infoBar.Append(a.repoPath)

	a.infoLabel = gtk.NewLabel("")
	a.infoLabel.SetXAlign(1)
	a.infoLabel.AddCSSClass("dim")
	infoBar.Append(a.infoLabel)

	box.Append(infoBar)

	// Tab switcher
	switcher := gtk.NewStackSwitcher()
	box.Append(switcher)

	a.stack = gtk.NewStack()
	a.stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	a.stack.SetTransitionDuration(160)
	a.stack.SetVExpand(true)
	a.stack.SetHExpand(true)
	switcher.SetStack(a.stack)

	a.stack.AddTitled(a.buildLogAndDiff(), "history", "History")
	a.stack.AddTitled(a.buildBranchesView(), "branches", "Branches")

	emptyWrap := gtk.NewBox(gtk.OrientationVertical, 0)
	emptyWrap.SetVExpand(true)
	emptyWrap.SetHExpand(true)
	emptyWrap.SetVAlign(gtk.AlignCenter)
	emptyWrap.SetHAlign(gtk.AlignCenter)
	a.emptyLabel = gtk.NewLabel("Select a repository")
	a.emptyLabel.AddCSSClass("empty")
	emptyWrap.Append(a.emptyLabel)

	a.stack.AddTitled(emptyWrap, "empty", "")
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
	diffScroll.AddCSSClass("diff-area")

	a.diffBuf = gtk.NewTextBuffer(nil)
	a.diffView = gtk.NewTextViewWithBuffer(a.diffBuf)
	a.diffView.SetEditable(false)
	a.diffView.SetCursorVisible(false)
	a.diffView.SetMonospace(true)
	a.diffView.SetTopMargin(12)
	a.diffView.SetBottomMargin(12)
	a.diffView.SetLeftMargin(16)
	a.diffView.SetRightMargin(16)
	a.diffView.AddCSSClass("diff-view")
	diffScroll.SetChild(a.diffView)

	paned.SetStartChild(logScroll)
	paned.SetEndChild(diffScroll)
	paned.SetPosition(270)
	return paned
}

func (a *App) buildBranchesView() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)

	newBranchBtn := gtk.NewButtonWithLabel("+ New")
	newBranchBtn.ConnectClicked(func() { a.openNewBranchDialog() })

	toolbar := gtk.NewBox(gtk.OrientationHorizontal, 6)
	toolbar.SetMarginTop(10)
	toolbar.SetMarginBottom(8)
	toolbar.SetMarginStart(12)
	toolbar.SetMarginEnd(12)

	a.branchSearch = gtk.NewSearchEntry()
	a.branchSearch.SetPlaceholderText("Filter branches…")
	a.branchSearch.SetHExpand(true)
	a.branchSearch.ConnectSearchChanged(func() { a.populateBranches() })
	toolbar.Append(a.branchSearch)
	toolbar.Append(newBranchBtn)
	box.Append(toolbar)

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	a.branchListBox = gtk.NewListBox()
	a.branchListBox.SetSelectionMode(gtk.SelectionNone)
	scroll.SetChild(a.branchListBox)
	box.Append(scroll)
	return box
}

// ── Commit strip ──────────────────────────────────────────────────

func (a *App) buildCommitStrip() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.AddCSSClass("commit-strip")

	a.commitEntry = gtk.NewEntry()
	a.commitEntry.SetPlaceholderText("Commit message…")
	a.commitEntry.SetHExpand(true)
	a.commitEntry.ConnectChanged(func() { a.updateCommitButton() })
	box.Append(a.commitEntry)

	a.commitButton = gtk.NewButtonWithLabel("Commit")
	a.commitButton.AddCSSClass("commit-button-custom")
	a.commitButton.SetSensitive(false)
	a.commitButton.ConnectClicked(func() {
		if a.state == nil {
			return
		}
		msg := strings.TrimSpace(a.commitEntry.Text())
		if msg == "" {
			return
		}
		a.setInfo("Committing…")
		go func(repo, message string) {
			err := DoCommit(repo, message)
			glib.IdleAdd(func() {
				if err != nil {
					a.setInfoErr(err.Error())
					return
				}
				a.commitEntry.SetText("")
				a.setInfoOk("Committed")
				a.doReload(true)
			})
		}(a.state.Path, msg)
	})
	box.Append(a.commitButton)
	return box
}

// ── Status strip ──────────────────────────────────────────────────

func (a *App) buildStatusStrip() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	box.AddCSSClass("status-strip")

	a.branchLabel = gtk.NewLabel("")
	a.aheadLabel = gtk.NewLabel("")
	a.aheadLabel.AddCSSClass("ahead-behind")
	a.statsLabel = gtk.NewLabel("")

	box.Append(a.branchLabel)
	box.Append(a.aheadLabel)
	box.Append(a.statsLabel)

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	box.Append(spacer)
	return box
}

// ── Info helpers ──────────────────────────────────────────────────

func (a *App) setInfo(t string) {
	if a.infoLabel == nil {
		return
	}
	a.infoLabel.SetText(t)
	a.infoLabel.RemoveCSSClass("info-ok")
	a.infoLabel.RemoveCSSClass("info-err")
	a.infoLabel.AddCSSClass("dim")
}

func (a *App) setInfoOk(t string) {
	if a.infoLabel == nil {
		return
	}
	a.infoLabel.SetText(t)
	a.infoLabel.RemoveCSSClass("dim")
	a.infoLabel.RemoveCSSClass("info-err")
	a.infoLabel.AddCSSClass("info-ok")
}

func (a *App) setInfoErr(t string) {
	if a.infoLabel == nil {
		return
	}
	a.infoLabel.SetText(t)
	a.infoLabel.RemoveCSSClass("dim")
	a.infoLabel.RemoveCSSClass("info-ok")
	a.infoLabel.AddCSSClass("info-err")
}

func (a *App) doReload(showDefault bool) {
	if a.cfg.Features.AsyncStateReload {
		a.reloadStateAsync(showDefault)
	} else {
		a.reloadState(showDefault)
	}
}

// ── Repo list ─────────────────────────────────────────────────────

func (a *App) populateRepos() {
	clearListBox(a.repoListBox)
	if len(a.repos) == 0 {
		a.repoListBox.Append(a.makePlaceholderRow("No repositories found"))
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
		click.ConnectReleased(func(_ int, _, _ float64) { a.selectRepo(repo.Path) })
		row.AddController(click)
		a.repoListBox.Append(row)
	}
}

func (a *App) selectRepo(path string) {
	a.selectedRepoPath = path
	a.selectedFile = ""
	a.selectedCommit = ""

	idx := 0
	for i, r := range a.repos {
		if r.Path == path {
			idx = i
			break
		}
	}
	if row := a.repoListBox.RowAtIndex(idx); row != nil {
		if a.selectedRepoRow != nil {
			a.selectedRepoRow.RemoveCSSClass("selected")
		}
		row.AddCSSClass("selected")
		a.selectedRepoRow = row
	}
	a.doReload(true)
}

// ── State ─────────────────────────────────────────────────────────

func (a *App) reloadStateAsync(showDefault bool) {
	if a.selectedRepoPath == "" {
		return
	}
	if showDefault || a.state == nil {
		a.stack.SetVisibleChildName("empty")
		a.emptyLabel.SetText("Loading…")
	}
	repoPath := a.selectedRepoPath
	max := a.cfg.Behavior.MaxCommits
	go func() {
		ns := LoadRepoState(repoPath, max)
		glib.IdleAdd(func() {
			if ns == nil {
				a.stack.SetVisibleChildName("empty")
				a.emptyLabel.SetText("Failed to load repository")
				return
			}
			a.applyState(ns, showDefault)
		})
	}()
}

func (a *App) reloadState(showDefault bool) {
	if a.selectedRepoPath == "" {
		return
	}
	ns := LoadRepoState(a.selectedRepoPath, a.cfg.Behavior.MaxCommits)
	if ns == nil {
		a.stack.SetVisibleChildName("empty")
		a.emptyLabel.SetText("Failed to load repository")
		return
	}
	a.applyState(ns, showDefault)
}

func (a *App) applyState(ns *RepoState, showDefault bool) {
	commitsChanged := a.commitsChanged(ns.Commits)
	filesChanged := a.filesChanged(ns.Files)
	branchesChanged := a.branchesChanged(ns.Branches)
	a.state = ns

	if branchesChanged {
		a.bindBranchDrop()
		a.populateBranches()
	}
	if filesChanged {
		a.populateFiles()
	}
	if commitsChanged {
		a.populateCommits()
	}

	a.updateHeaderInfo()
	a.updateStatusStrip()
	a.updateRemoteSummary()

	if a.stack.VisibleChildName() == "empty" {
		a.stack.SetVisibleChildName("history")
	}

	if showDefault {
		if len(ns.Files) > 0 {
			f := ns.Files[0]
			a.selectedFile = f.Path
			a.selectedFileMode = f.Staged
			a.selectedCommit = ""
			a.loadFileDiff(f.Path, f.Staged)
		} else if len(ns.Commits) > 0 {
			c := ns.Commits[0]
			a.selectedCommit = c.Hash
			a.selectedFile = ""
			a.loadCommitDiff(c.Hash)
		} else {
			a.renderDiff("")
		}
	}
	a.updateCommitButton()
}

func (a *App) commitsChanged(nc []Commit) bool {
	if a.state == nil || len(a.state.Commits) != len(nc) {
		return true
	}
	for i := range nc {
		if i >= 50 {
			break
		}
		if a.state.Commits[i].Hash != nc[i].Hash {
			return true
		}
	}
	return false
}

func (a *App) filesChanged(nf []FileStatus) bool {
	if a.state == nil || len(a.state.Files) != len(nf) {
		return true
	}
	for i := range nf {
		if a.state.Files[i].Path != nf[i].Path ||
			a.state.Files[i].Staged != nf[i].Staged ||
			a.state.Files[i].IndexStatus != nf[i].IndexStatus ||
			a.state.Files[i].WorkStatus != nf[i].WorkStatus {
			return true
		}
	}
	return false
}

func (a *App) branchesChanged(nb []BranchInfo) bool {
	if a.state == nil || len(a.state.Branches) != len(nb) {
		return true
	}
	for i := range nb {
		if a.state.Branches[i].Name != nb[i].Name ||
			a.state.Branches[i].Current != nb[i].Current ||
			a.state.Branches[i].Hash != nb[i].Hash {
			return true
		}
	}
	return false
}

func (a *App) stashesChanged(ns []StashEntry) bool {
	if a.state == nil || len(a.state.Stashes) != len(ns) {
		return true
	}
	for i := range ns {
		if a.state.Stashes[i].Ref != ns[i].Ref {
			return true
		}
	}
	return false
}

// ── Branch dropdown ───────────────────────────────────────────────

func (a *App) bindBranchDrop() {
	if a.state == nil {
		return
	}
	var local []BranchInfo
	for _, b := range a.state.Branches {
		if !b.IsRemote {
			local = append(local, b)
		}
	}
	names := make([]string, 0, len(local))
	cur := uint(0)
	for i, b := range local {
		names = append(names, b.Name)
		if b.Current {
			cur = uint(i)
		}
	}
	a.branchDrop.SetModel(gtk.NewStringList(names))
	if len(names) > 0 {
		a.branchDrop.SetSelected(cur)
	}
	if a.branchDropBound {
		return
	}
	a.branchDropBound = true
	a.branchDrop.Connect("notify::selected", func() {
		if a.state == nil {
			return
		}
		sel := int(a.branchDrop.Selected())
		if sel < 0 || sel >= len(names) {
			return
		}
		target := names[sel]
		if target == a.state.Branch {
			return
		}
		a.setInfo("Switching to " + target + "…")
		go func(repo, branch string) {
			err := Checkout(repo, branch)
			glib.IdleAdd(func() {
				if err != nil {
					a.setInfoErr(err.Error())
				} else {
					a.setInfoOk("On " + branch)
				}
				a.doReload(true)
			})
		}(a.state.Path, target)
	})
}

// ── File list ─────────────────────────────────────────────────────

func (a *App) populateFiles() {
	clearListBox(a.fileListBox)
	a.selectedFileRow = nil

	if a.state == nil || len(a.state.Files) == 0 {
		a.fileListBox.Append(a.makePlaceholderRow("Working tree clean"))
		return
	}

	var staged, unstaged []FileStatus
	for _, f := range a.state.Files {
		if f.Staged {
			staged = append(staged, f)
		} else {
			unstaged = append(unstaged, f)
		}
	}
	if len(staged) > 0 {
		a.appendFileSection("Staged", staged, true)
	}
	if len(unstaged) > 0 {
		a.appendFileSection("Changes", unstaged, false)
	}
}

func (a *App) appendFileSection(title string, files []FileStatus, staged bool) {
	// Section header row — class on the ROW so GTK's bg override is caught
	hdrRow := gtk.NewListBoxRow()
	hdrRow.SetSelectable(false)
	hdrRow.AddCSSClass("section-header")

	hdrBox := gtk.NewBox(gtk.OrientationHorizontal, 6)
	hdrBox.AddCSSClass("sidebar-section")
	hdrBox.SetMarginTop(10)
	hdrBox.SetMarginBottom(5)
	hdrBox.SetMarginStart(14)
	hdrBox.SetMarginEnd(10)

	lbl := gtk.NewLabel(title)
	lbl.SetXAlign(0)
	lbl.SetHExpand(true)
	lbl.AddCSSClass("section-label")
	hdrBox.Append(lbl)

	countLbl := gtk.NewLabel(strconv.Itoa(len(files)))
	countLbl.AddCSSClass("section-count")
	hdrBox.Append(countLbl)

	hdrRow.SetChild(hdrBox)
	a.fileListBox.Append(hdrRow)

	for _, f := range files {
		f := f
		row := gtk.NewListBoxRow()
		row.AddCSSClass("file-row")

		box := gtk.NewBox(gtk.OrientationHorizontal, 6)
		box.AddCSSClass("file-row-box")

		statusLbl := gtk.NewLabel(statusMark(f))
		statusLbl.SetWidthChars(2)
		statusLbl.SetXAlign(0.5)
		switch statusKind(f) {
		case "A":
			statusLbl.AddCSSClass("status-added")
		case "M":
			statusLbl.AddCSSClass("status-modified")
		case "D":
			statusLbl.AddCSSClass("status-removed")
		default:
			statusLbl.AddCSSClass("status-unknown")
		}

		name := gtk.NewLabel(f.Path)
		name.SetHExpand(true)
		name.SetXAlign(0)
		name.SetEllipsize(3)

		// Stage/unstage toggle
		toggleLabel := "+"
		if staged {
			toggleLabel = "−"
		}
		toggle := gtk.NewButtonWithLabel(toggleLabel)
		toggle.AddCSSClass("flat")
		toggle.SetSizeRequest(26, 26)
		toggle.ConnectClicked(func() {
			if a.state == nil {
				return
			}
			go func(repo, file string, isStaged bool) {
				if isStaged {
					_ = UnstageFile(repo, file)
				} else {
					_ = StageFile(repo, file)
				}
				glib.IdleAdd(func() {
					a.doReload(false)
					a.loadFileDiff(file, !isStaged)
				})
			}(a.state.Path, f.Path, staged)
		})

		box.Append(statusLbl)
		box.Append(name)
		box.Append(toggle)
		row.SetChild(box)

		click := gtk.NewGestureClick()
		click.ConnectReleased(func(_ int, _, _ float64) {
			if a.state == nil {
				return
			}
			a.selectedFile = f.Path
			a.selectedFileMode = staged
			a.selectedCommit = ""
			a.loadFileDiff(f.Path, staged)
			a.markSelectedRow(a.fileListBox, row)
		})
		row.AddController(click)
		a.fileListBox.Append(row)
	}
}

// ── Commit list ───────────────────────────────────────────────────

func (a *App) populateCommits() {
	clearListBox(a.commitListBox)
	a.selectedCommitRow = nil

	if a.state == nil {
		return
	}

	filter := strings.ToLower(strings.TrimSpace(a.searchEntry.Text()))
	count := 0

	for _, c := range a.state.Commits {
		if filter != "" && c.IsCommit {
			if !strings.Contains(strings.ToLower(c.Subject+" "+c.Author+" "+c.ShortHash), filter) {
				continue
			}
		}
		c := c

		row := gtk.NewListBoxRow()
		row.AddCSSClass("commit-row")

		box := gtk.NewBox(gtk.OrientationHorizontal, 8)
		box.AddCSSClass("commit-row-box")

		graph := gtk.NewLabel(c.Graph)
		graph.SetXAlign(0)
		graph.AddCSSClass("commit-graph")
		box.Append(graph)

		if !c.IsCommit {
			row.SetSelectable(false)
			row.SetChild(box)
			a.commitListBox.Append(row)
			continue
		}

		hash := gtk.NewLabel(c.ShortHash)
		hash.SetXAlign(0)
		hash.SetWidthChars(8)
		hash.AddCSSClass("commit-hash")
		box.Append(hash)

		wrap := gtk.NewBox(gtk.OrientationVertical, 2)
		wrap.SetHExpand(true)

		subjRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
		subj := gtk.NewLabel(c.Subject)
		subj.SetXAlign(0)
		subj.SetEllipsize(3)
		subjRow.Append(subj)

		if c.RefNames != "" {
			for _, ref := range strings.Split(c.RefNames, ",") {
				if lbl := a.buildRefLabel(ref); lbl != nil {
					subjRow.Append(lbl)
				}
			}
		}
		wrap.Append(subjRow)

		var metaParts []string
		if a.cfg.Features.ShowCommitAuthors {
			metaParts = append(metaParts, c.Author)
		}
		if a.cfg.Features.ShowCommitDates {
			metaParts = append(metaParts, c.DateRel)
		}
		if len(metaParts) > 0 {
			meta := gtk.NewLabel(strings.Join(metaParts, " · "))
			meta.SetXAlign(0)
			meta.AddCSSClass("commit-meta")
			wrap.Append(meta)
		}

		box.Append(wrap)
		row.SetChild(box)

		click := gtk.NewGestureClick()
		click.ConnectReleased(func(_ int, _, _ float64) {
			if a.state == nil {
				return
			}
			a.selectedCommit = c.Hash
			a.selectedFile = ""
			a.loadCommitDiff(c.Hash)
			a.markSelectedRow(a.commitListBox, row)
		})
		row.AddController(click)
		a.commitListBox.Append(row)
		count++
	}

	if count == 0 && filter != "" {
		a.commitListBox.Append(a.makePlaceholderRow("No commits match"))
	}
}

func (a *App) buildRefLabel(ref string) *gtk.Widget {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	kind, text := "branch", ref
	if strings.HasPrefix(ref, "HEAD -> ") {
		text = strings.TrimPrefix(ref, "HEAD -> ")
		kind = "head"
	} else if strings.HasPrefix(ref, "tag: ") {
		text = strings.TrimPrefix(ref, "tag: ")
		kind = "tag"
	} else if strings.Contains(ref, "/") {
		kind = "remote"
	}
	lbl := gtk.NewLabel(text)
	lbl.SetMarginStart(3)
	lbl.SetMarginEnd(3)
	lbl.AddCSSClass("ref-label")
	lbl.AddCSSClass("ref-" + kind)
	return &lbl.Widget
}

// ── Branch panel ──────────────────────────────────────────────────

func (a *App) populateBranches() {
	clearListBox(a.branchListBox)
	if a.state == nil {
		return
	}
	filter := strings.ToLower(strings.TrimSpace(a.branchSearch.Text()))

	var local, remote []BranchInfo
	for _, b := range a.state.Branches {
		if filter != "" && !strings.Contains(strings.ToLower(b.Name), filter) {
			continue
		}
		if b.IsRemote {
			remote = append(remote, b)
		} else {
			local = append(local, b)
		}
	}
	if len(local) > 0 {
		a.appendBranchSection("Local", local)
	}
	if len(remote) > 0 {
		a.appendBranchSection("Remote", remote)
	}
	if len(local)+len(remote) == 0 {
		a.branchListBox.Append(a.makePlaceholderRow("No branches match"))
	}
}

func (a *App) appendBranchSection(title string, branches []BranchInfo) {
	hdrRow := gtk.NewListBoxRow()
	hdrRow.SetSelectable(false)
	hdrRow.AddCSSClass("section-header")

	hdrBox := gtk.NewBox(gtk.OrientationHorizontal, 6)
	hdrBox.AddCSSClass("sidebar-section")
	hdrBox.SetMarginTop(10)
	hdrBox.SetMarginBottom(5)
	hdrBox.SetMarginStart(14)
	hdrBox.SetMarginEnd(10)

	lbl := gtk.NewLabel(title)
	lbl.SetXAlign(0)
	lbl.SetHExpand(true)
	lbl.AddCSSClass("section-label")
	hdrBox.Append(lbl)

	countLbl := gtk.NewLabel(strconv.Itoa(len(branches)))
	countLbl.AddCSSClass("section-count")
	hdrBox.Append(countLbl)

	hdrRow.SetChild(hdrBox)
	a.branchListBox.Append(hdrRow)

	for _, b := range branches {
		b := b
		row := gtk.NewListBoxRow()
		row.AddCSSClass("branch-row")

		outer := gtk.NewBox(gtk.OrientationHorizontal, 8)
		outer.AddCSSClass("branch-row-box")

		info := gtk.NewBox(gtk.OrientationVertical, 3)
		info.SetHExpand(true)

		nameRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
		name := gtk.NewLabel(b.Name)
		name.SetXAlign(0)
		name.AddCSSClass("repo-name")
		nameRow.Append(name)

		if b.Current {
			badge := gtk.NewLabel("current")
			badge.AddCSSClass("badge-current")
			nameRow.Append(badge)
		}
		if b.Ahead > 0 || b.Behind > 0 {
			ab := gtk.NewLabel(fmt.Sprintf("↑%d ↓%d", b.Ahead, b.Behind))
			ab.AddCSSClass("ahead-behind")
			nameRow.Append(ab)
		}
		info.Append(nameRow)

		var metaParts []string
		if b.Hash != "" {
			metaParts = append(metaParts, b.Hash)
		}
		if b.Subject != "" {
			metaParts = append(metaParts, b.Subject)
		}
		if b.Date != "" {
			metaParts = append(metaParts, b.Date)
		}
		if len(metaParts) > 0 {
			meta := gtk.NewLabel(strings.Join(metaParts, " · "))
			meta.AddCSSClass("commit-meta")
			meta.SetXAlign(0)
			meta.SetEllipsize(3)
			info.Append(meta)
		}
		if b.Remote != "" {
			up := gtk.NewLabel("↑ " + b.Remote)
			up.AddCSSClass("dim")
			up.SetXAlign(0)
			info.Append(up)
		}
		outer.Append(info)

		acts := gtk.NewBox(gtk.OrientationHorizontal, 4)
		acts.SetVAlign(gtk.AlignCenter)

		if !b.Current && !b.IsRemote {
			coBtn := gtk.NewButtonWithLabel("Checkout")
			coBtn.ConnectClicked(func() {
				a.setInfo("Checking out " + b.Name + "…")
				go func(repo, branch string) {
					err := Checkout(repo, branch)
					glib.IdleAdd(func() {
						if err != nil {
							a.setInfoErr(err.Error())
						} else {
							a.setInfoOk("On " + branch)
						}
						a.doReload(true)
					})
				}(a.state.Path, b.Name)
			})
			acts.Append(coBtn)

			mergeBtn := gtk.NewButtonWithLabel("Merge")
			mergeBtn.ConnectClicked(func() {
				a.confirmDialog(
					"Merge "+b.Name,
					"Merge branch '"+b.Name+"' into the current branch?",
					false,
					func() {
						a.setInfo("Merging " + b.Name + "…")
						go func(repo, branch string) {
							err := MergeBranch(repo, branch)
							glib.IdleAdd(func() {
								if err != nil {
									a.setInfoErr(err.Error())
								} else {
									a.setInfoOk("Merged " + branch)
								}
								a.doReload(true)
							})
						}(a.state.Path, b.Name)
					},
				)
			})
			acts.Append(mergeBtn)

			delBtn := gtk.NewButtonWithLabel("Delete")
			delBtn.AddCSSClass("destructive-action")
			delBtn.ConnectClicked(func() {
				a.confirmDialog(
					"Delete branch",
					"Delete '"+b.Name+"'? This cannot be undone.",
					true,
					func() {
						go func(repo, branch string) {
							err := DeleteBranch(repo, branch, false)
							glib.IdleAdd(func() {
								if err != nil {
									a.setInfoErr(err.Error())
								} else {
									a.setInfoOk("Deleted " + branch)
								}
								a.doReload(true)
							})
						}(a.state.Path, b.Name)
					},
				)
			})
			acts.Append(delBtn)
		}

		if b.IsRemote {
			trackBtn := gtk.NewButtonWithLabel("Track")
			trackBtn.ConnectClicked(func() {
				localName := b.Name
				if idx := strings.LastIndex(localName, "/"); idx >= 0 {
					localName = localName[idx+1:]
				}
				a.setInfo("Tracking " + b.Name + "…")
				go func(repo, remote, local string) {
					err := CheckoutNewBranch(repo, local)
					if err == nil {
						_ = gitCmd2(repo, "branch", "--set-upstream-to", remote, local)
					}
					glib.IdleAdd(func() {
						if err != nil {
							a.setInfoErr(err.Error())
						} else {
							a.setInfoOk("Tracking " + local)
						}
						a.doReload(true)
					})
				}(a.state.Path, b.Name, localName)
			})
			acts.Append(trackBtn)
		}

		outer.Append(acts)
		row.SetChild(outer)
		a.branchListBox.Append(row)
	}
}

// ── Stash panel ───────────────────────────────────────────────────

func (a *App) openStashPanel() {
	if a.state == nil {
		return
	}

	content := gtk.NewBox(gtk.OrientationVertical, 0)

	// Save row
	saveBar := gtk.NewBox(gtk.OrientationHorizontal, 8)
	saveBar.SetMarginTop(10)
	saveBar.SetMarginBottom(10)
	saveBar.SetMarginStart(14)
	saveBar.SetMarginEnd(14)

	msgEntry := gtk.NewEntry()
	msgEntry.SetPlaceholderText("Stash message (optional)…")
	msgEntry.SetHExpand(true)
	saveBar.Append(msgEntry)

	saveBtn := gtk.NewButtonWithLabel("Stash Changes")
	saveBtn.AddCSSClass("suggested-action")
	saveBtn.ConnectClicked(func() {
		if a.state == nil {
			return
		}
		msg := strings.TrimSpace(msgEntry.Text())
		go func(repo, message string) {
			err := StashSave(repo, message)
			glib.IdleAdd(func() {
				if err != nil {
					a.setInfoErr(err.Error())
				} else {
					msgEntry.SetText("")
					a.setInfoOk("Stashed")
				}
				a.doReload(false)
				a.populateStashList()
			})
		}(a.state.Path, msg)
	})
	saveBar.Append(saveBtn)
	content.Append(saveBar)

	sep := gtk.NewSeparator(gtk.OrientationHorizontal)
	content.Append(sep)

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	scroll.SetSizeRequest(-1, 400)

	a.stashListBox = gtk.NewListBox()
	a.stashListBox.SetSelectionMode(gtk.SelectionNone)
	scroll.SetChild(a.stashListBox)
	content.Append(scroll)

	a.populateStashList()

	card := a.buildOverlayCard("Stash Manager", 800, content)
	a.showOverlay(card)
}

func (a *App) populateStashList() {
	if a.stashListBox == nil {
		return
	}
	clearListBox(a.stashListBox)
	if a.state == nil || len(a.state.Stashes) == 0 {
		a.stashListBox.Append(a.makePlaceholderRow("No stashes"))
		return
	}
	for _, s := range a.state.Stashes {
		s := s
		row := gtk.NewListBoxRow()
		row.AddCSSClass("stash-row")

		box := gtk.NewBox(gtk.OrientationHorizontal, 10)
		box.AddCSSClass("stash-row-box")

		idx := gtk.NewLabel(s.Ref)
		idx.AddCSSClass("stash-index")
		idx.SetXAlign(0)
		idx.SetSizeRequest(90, -1)
		box.Append(idx)

		info := gtk.NewBox(gtk.OrientationVertical, 2)
		info.SetHExpand(true)

		msg := gtk.NewLabel(s.Message)
		msg.SetXAlign(0)
		msg.AddCSSClass("stash-msg")
		msg.SetEllipsize(3)
		info.Append(msg)

		if s.Date != "" {
			dl := gtk.NewLabel(s.Date)
			dl.SetXAlign(0)
			dl.AddCSSClass("stash-meta")
			info.Append(dl)
		}
		box.Append(info)

		// Actions
		viewBtn := gtk.NewButtonWithLabel("Diff")
		viewBtn.AddCSSClass("flat")
		viewBtn.ConnectClicked(func() {
			if a.state == nil {
				return
			}
			diff := StashShow(a.state.Path, s.Index)
			a.renderDiff(diff)
			a.hideOverlay()
			a.stack.SetVisibleChildName("history")
		})
		box.Append(viewBtn)

		applyBtn := gtk.NewButtonWithLabel("Apply")
		applyBtn.ConnectClicked(func() {
			if a.state == nil {
				return
			}
			go func(repo string, i int) {
				err := StashApply(repo, i)
				glib.IdleAdd(func() {
					if err != nil {
						a.setInfoErr(err.Error())
					} else {
						a.setInfoOk("Stash applied")
					}
					a.doReload(true)
					a.populateStashList()
				})
			}(a.state.Path, s.Index)
		})
		box.Append(applyBtn)

		popBtn := gtk.NewButtonWithLabel("Pop")
		popBtn.AddCSSClass("suggested-action")
		popBtn.ConnectClicked(func() {
			if a.state == nil {
				return
			}
			go func(repo string, i int) {
				err := StashPop(repo, i)
				glib.IdleAdd(func() {
					if err != nil {
						a.setInfoErr(err.Error())
					} else {
						a.setInfoOk("Stash popped")
					}
					a.doReload(true)
					a.populateStashList()
				})
			}(a.state.Path, s.Index)
		})
		box.Append(popBtn)

		dropBtn := gtk.NewButtonWithLabel("Drop")
		dropBtn.AddCSSClass("destructive-action")
		dropBtn.ConnectClicked(func() {
			a.confirmDialog(
				"Drop stash?",
				s.Message,
				true,
				func() {
					go func(repo string, i int) {
						err := StashDrop(repo, i)
						glib.IdleAdd(func() {
							if err != nil {
								a.setInfoErr(err.Error())
							} else {
								a.setInfoOk("Stash dropped")
							}
							a.doReload(false)
							a.populateStashList()
						})
					}(a.state.Path, s.Index)
				},
			)
		})
		box.Append(dropBtn)

		row.SetChild(box)
		a.stashListBox.Append(row)
	}
}

// ── Repo config panel ─────────────────────────────────────────────

func (a *App) openRepoConfigPanel() {
	if a.state == nil || !a.cfg.Features.RepoConfigDialog {
		return
	}

	content := gtk.NewBox(gtk.OrientationVertical, 0)

	// Repo info
	infoBox := gtk.NewBox(gtk.OrientationVertical, 4)
	infoBox.SetMarginTop(12)
	infoBox.SetMarginBottom(10)
	infoBox.SetMarginStart(14)
	infoBox.SetMarginEnd(14)

	repoLbl := gtk.NewLabel(a.state.Name)
	repoLbl.SetXAlign(0)
	repoLbl.AddCSSClass("repo-name")
	infoBox.Append(repoLbl)

	pathLbl := gtk.NewLabel(a.state.Path)
	pathLbl.SetXAlign(0)
	pathLbl.AddCSSClass("repo-path")
	infoBox.Append(pathLbl)

	upstream := GetBranchUpstream(a.state.Path, a.state.Branch)
	branchText := "Branch: " + a.state.Branch
	if upstream != "" {
		branchText += "  →  " + upstream
	}
	bl := gtk.NewLabel(branchText)
	bl.SetXAlign(0)
	bl.AddCSSClass("dim")
	infoBox.Append(bl)
	content.Append(infoBox)

	content.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	secLbl := gtk.NewLabel("Remotes")
	secLbl.SetXAlign(0)
	secLbl.SetMarginTop(10)
	secLbl.SetMarginBottom(4)
	secLbl.SetMarginStart(14)
	secLbl.AddCSSClass("section-label")
	content.Append(secLbl)

	scroll := gtk.NewScrolledWindow()
	scroll.SetVExpand(true)
	scroll.SetSizeRequest(-1, 260)

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	scroll.SetChild(list)
	content.Append(scroll)

	var refreshList func()
	refreshList = func() {
		clearListBox(list)
		remotes := GetRemotes(a.state.Path)
		if len(remotes) == 0 {
			list.Append(a.makePlaceholderRow("No remotes configured"))
			return
		}
		for _, remote := range remotes {
			remote := remote
			row := gtk.NewListBoxRow()
			box := gtk.NewBox(gtk.OrientationVertical, 6)
			box.SetMarginTop(10)
			box.SetMarginBottom(10)
			box.SetMarginStart(14)
			box.SetMarginEnd(14)

			head := gtk.NewBox(gtk.OrientationHorizontal, 8)
			name := gtk.NewLabel(remote.Name)
			name.SetXAlign(0)
			name.SetHExpand(true)
			name.AddCSSClass("repo-name")
			head.Append(name)

			renameBtn := gtk.NewButtonWithLabel("Rename")
			renameBtn.ConnectClicked(func() {
				a.promptDialog("Rename Remote", "New name", remote.Name, func(v string) {
					if v == "" || v == remote.Name {
						return
					}
					if err := RenameRemote(a.state.Path, remote.Name, v); err != nil {
						a.setInfoErr(err.Error())
						return
					}
					refreshList()
					a.doReload(false)
				})
			})
			head.Append(renameBtn)

			urlBtn := gtk.NewButtonWithLabel("Set URL")
			urlBtn.ConnectClicked(func() {
				a.promptDialog("Set Remote URL", "URL", remote.FetchURL, func(v string) {
					if v == "" {
						return
					}
					if err := SetRemoteURL(a.state.Path, remote.Name, v); err != nil {
						a.setInfoErr(err.Error())
						return
					}
					refreshList()
					a.doReload(false)
				})
			})
			head.Append(urlBtn)

			rmBtn := gtk.NewButtonWithLabel("Remove")
			rmBtn.AddCSSClass("destructive-action")
			rmBtn.ConnectClicked(func() {
				a.confirmDialog(
					"Remove remote",
					"Remove '"+remote.Name+"'?",
					true,
					func() {
						if err := RemoveRemote(a.state.Path, remote.Name); err != nil {
							a.setInfoErr(err.Error())
							return
						}
						refreshList()
						a.doReload(false)
					},
				)
			})
			head.Append(rmBtn)
			box.Append(head)

			fetchLbl := gtk.NewLabel("Fetch: " + remote.FetchURL)
			fetchLbl.SetXAlign(0)
			fetchLbl.SetSelectable(true)
			fetchLbl.AddCSSClass("repo-path")
			box.Append(fetchLbl)

			pushLbl := gtk.NewLabel("Push:  " + remote.PushURL)
			pushLbl.SetXAlign(0)
			pushLbl.SetSelectable(true)
			pushLbl.AddCSSClass("repo-path")
			box.Append(pushLbl)

			row.SetChild(box)
			list.Append(row)
		}
	}

	// Add remote
	addBar := gtk.NewBox(gtk.OrientationHorizontal, 8)
	addBar.SetMarginTop(8)
	addBar.SetMarginBottom(8)
	addBar.SetMarginStart(14)
	addBar.SetMarginEnd(14)

	addName := gtk.NewEntry()
	addName.SetPlaceholderText("name")
	addName.SetSizeRequest(110, -1)

	addURL := gtk.NewEntry()
	addURL.SetPlaceholderText("url")
	addURL.SetHExpand(true)

	addBtn := gtk.NewButtonWithLabel("Add")
	addBtn.AddCSSClass("suggested-action")
	addBtn.ConnectClicked(func() {
		n := strings.TrimSpace(addName.Text())
		u := strings.TrimSpace(addURL.Text())
		if n == "" || u == "" {
			return
		}
		if err := AddRemote(a.state.Path, n, u); err != nil {
			a.setInfoErr(err.Error())
			return
		}
		addName.SetText("")
		addURL.SetText("")
		refreshList()
		a.doReload(false)
	})

	addBar.Append(addName)
	addBar.Append(addURL)
	addBar.Append(addBtn)
	content.Append(addBar)
	refreshList()

	card := a.buildOverlayCard("Repository Remotes", 760, content)
	a.showOverlay(card)
}

// ── New branch dialog ─────────────────────────────────────────────

func (a *App) openNewBranchDialog() {
	if a.state == nil {
		return
	}
	content := gtk.NewBox(gtk.OrientationVertical, 14)
	content.SetMarginTop(16)
	content.SetMarginBottom(16)
	content.SetMarginStart(16)
	content.SetMarginEnd(16)

	entry := gtk.NewEntry()
	entry.SetPlaceholderText("Branch name")
	content.Append(entry)

	btnRow := gtk.NewBox(gtk.OrientationHorizontal, 8)
	btnRow.SetHAlign(gtk.AlignEnd)

	cancelBtn := gtk.NewButtonWithLabel("Cancel")
	cancelBtn.ConnectClicked(func() { a.hideOverlay() })
	btnRow.Append(cancelBtn)

	createBtn := gtk.NewButtonWithLabel("Create & Checkout")
	createBtn.AddCSSClass("suggested-action")
	createBtn.ConnectClicked(func() {
		name := strings.TrimSpace(entry.Text())
		if name == "" {
			return
		}
		go func(repo, branch string) {
			err := CheckoutNewBranch(repo, branch)
			glib.IdleAdd(func() {
				if err != nil {
					a.setInfoErr(err.Error())
				} else {
					a.setInfoOk("Created " + branch)
				}
				a.hideOverlay()
				a.doReload(true)
			})
		}(a.state.Path, name)
	})
	btnRow.Append(createBtn)
	content.Append(btnRow)

	card := a.buildOverlayCard("New Branch", 420, content)
	a.showOverlay(card)
}

// ── Header info ───────────────────────────────────────────────────

func (a *App) updateHeaderInfo() {
	if a.state == nil {
		a.repoTitle.SetText("")
		a.repoPath.SetText("")
		a.setInfo("")
		return
	}
	a.repoTitle.SetText(a.state.Name)
	a.repoPath.SetText(a.state.Path)

	var parts []string
	if n := len(a.state.Branches); n > 0 {
		parts = append(parts, strconv.Itoa(n)+" branches")
	}
	if n := len(a.state.Remotes); n > 0 {
		parts = append(parts, strconv.Itoa(n)+" remotes")
	}
	if n := len(a.state.Commits); n > 0 {
		parts = append(parts, strconv.Itoa(n)+" commits")
	}
	if n := len(a.state.Stashes); n > 0 {
		parts = append(parts, strconv.Itoa(n)+" stashes")
	}
	a.setInfo(strings.Join(parts, " · "))
}

func (a *App) updateRemoteSummary() {
	if a.remoteLabel == nil || a.state == nil {
		return
	}
	if !a.cfg.Features.ShowRemoteSummary || len(a.state.Remotes) == 0 {
		a.remoteLabel.SetText("")
		return
	}
	names := make([]string, 0, len(a.state.Remotes))
	for _, r := range a.state.Remotes {
		names = append(names, r.Name)
	}
	a.remoteLabel.SetText(strings.Join(names, ", "))
}

func (a *App) updateStatusStrip() {
	if a.state == nil {
		a.branchLabel.SetText("")
		a.aheadLabel.SetText("")
		a.statsLabel.SetText("")
		return
	}
	staged, changed := 0, 0
	for _, f := range a.state.Files {
		if f.Staged {
			staged++
		} else {
			changed++
		}
	}

	a.branchLabel.SetText(" " + a.state.Branch)

	if a.state.Ahead > 0 || a.state.Behind > 0 {
		a.aheadLabel.SetText(fmt.Sprintf("↑%d ↓%d", a.state.Ahead, a.state.Behind))
	} else {
		a.aheadLabel.SetText("")
	}

	var parts []string
	if staged > 0 {
		parts = append(parts, strconv.Itoa(staged)+" staged")
	}
	if changed > 0 {
		parts = append(parts, strconv.Itoa(changed)+" changed")
	}
	if n := len(a.state.Stashes); n > 0 {
		parts = append(parts, strconv.Itoa(n)+" stashed")
	}
	a.statsLabel.SetText(strings.Join(parts, " · "))
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

// ── Diff ──────────────────────────────────────────────────────────

func (a *App) renderDiff(diff string) {
	a.diffBuf.SetText("")
	if strings.TrimSpace(diff) == "" {
		a.diffBuf.SetText("No changes to display")
		return
	}

	tt := a.diffBuf.TagTable()
	ensure := func(name, fg string) {
		if tt.Lookup(name) != nil {
			return
		}
		tag := gtk.NewTextTag(name)
		tag.SetObjectProperty("foreground", fg)
		tt.Add(tag)
	}
	ensure("added", a.cfg.Colors.Added)
	ensure("removed", a.cfg.Colors.Removed)
	ensure("header", a.cfg.Colors.Accent)
	ensure("hunk", a.cfg.Colors.TextDim)
	ensure("normal", a.cfg.Colors.Text)
	ensure("modified", a.cfg.Colors.Modified)

	for _, line := range strings.Split(diff, "\n") {
		start := a.diffBuf.EndIter()
		a.diffBuf.Insert(start, line+"\n")
		end := a.diffBuf.EndIter()

		tag := "normal"
		switch {
		case strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
			tag = "header"
		case strings.HasPrefix(line, "@@"):
			tag = "hunk"
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			tag = "added"
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			tag = "removed"
		case strings.HasPrefix(line, "rename "), strings.HasPrefix(line, "similarity "):
			tag = "modified"
		}

		lineStart := *end
		lineStart.BackwardChars(len([]rune(line + "\n")))
		a.diffBuf.ApplyTagByName(tag, &lineStart, end)
	}
}

func (a *App) nextDiffRequestID() uint64 {
	return atomic.AddUint64(&a.diffReqID, 1)
}

func (a *App) loadFileDiff(path string, staged bool) {
	if a.state == nil {
		return
	}
	if !a.cfg.Features.AsyncDiffLoading {
		a.renderDiff(GetFileDiff(a.state.Path, path, staged))
		return
	}
	id := a.nextDiffRequestID()
	repo := a.state.Path
	a.renderDiff("Loading…")
	go func() {
		d := GetFileDiff(repo, path, staged)
		glib.IdleAdd(func() {
			if id == atomic.LoadUint64(&a.diffReqID) {
				a.renderDiff(d)
			}
		})
	}()
}

func (a *App) loadCommitDiff(hash string) {
	if a.state == nil {
		return
	}
	if !a.cfg.Features.AsyncDiffLoading {
		a.renderDiff(GetCommitDiff(a.state.Path, hash))
		return
	}
	id := a.nextDiffRequestID()
	repo := a.state.Path
	a.renderDiff("Loading…")
	go func() {
		d := GetCommitDiff(repo, hash)
		glib.IdleAdd(func() {
			if id == atomic.LoadUint64(&a.diffReqID) {
				a.renderDiff(d)
			}
		})
	}()
}

// ── Utilities ─────────────────────────────────────────────────────

func (a *App) markSelectedRow(list *gtk.ListBox, row *gtk.ListBoxRow) {
	if list == a.fileListBox {
		if a.selectedFileRow != nil {
			a.selectedFileRow.RemoveCSSClass("selected")
		}
		row.AddCSSClass("selected")
		a.selectedFileRow = row
		return
	}
	if list == a.commitListBox {
		if a.selectedCommitRow != nil {
			a.selectedCommitRow.RemoveCSSClass("selected")
		}
		row.AddCSSClass("selected")
		a.selectedCommitRow = row
	}
}

func (a *App) makePlaceholderRow(text string) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.SetSelectable(false)
	lbl := gtk.NewLabel(text)
	lbl.SetXAlign(0)
	lbl.SetMarginTop(12)
	lbl.SetMarginBottom(12)
	lbl.SetMarginStart(14)
	lbl.AddCSSClass("dim")
	row.SetChild(lbl)
	return row
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
	switch statusKind(f) {
	case "A":
		return "A"
	case "M":
		return "M"
	case "D":
		return "D"
	case "?":
		return "U"
	default:
		return "·"
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

func gitCmd2(repoPath string, args ...string) error {
	_, err := gitCmd(repoPath, args...)
	return err
}

// Kept for backward compat
func (a *App) openRepoConfigDialog() { a.openRepoConfigPanel() }
func (a *App) populateStashes()      { a.populateStashList() }

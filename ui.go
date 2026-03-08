package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

//go:embed style.css
var defaultCSS string

type App struct {
	cfg   Config
	app   *gtk.Application
	win   *gtk.ApplicationWindow

	repos []RepoInfo
	state *RepoState

	rootOverlay   *gtk.Overlay
	overlayBox    *gtk.Box
	overlayReveal *gtk.Revealer

	repoListBox   *gtk.ListBox
	fileListBox   *gtk.ListBox
	commitListBox *gtk.ListBox
	branchListBox *gtk.ListBox
	stashListBox  *gtk.ListBox

	diffView      *gtk.Box
	diffScroll    *gtk.ScrolledWindow
	diffBuf       *gtk.TextBuffer
	diffReqID     uint64
	showSplit     bool
	splitView     *gtk.Box
	diffViewLeft  *gtk.TextView
	diffBufLeft   *gtk.TextBuffer
	diffViewRight *gtk.TextView
	diffBufRight  *gtk.TextBuffer

	commitHeader      *gtk.Box
	commitAuthor      *gtk.Label
	commitHashFull    *gtk.Label
	commitDateFull    *gtk.Label
	commitSubjectBold *gtk.Label

	searchEntry  *gtk.SearchEntry
	branchSearch *gtk.SearchEntry
	branchDrop   *gtk.DropDown
	commitEntry  *gtk.Entry
	commitButton   *gtk.Button
	splitToggleBtn *gtk.Button
	expandAllBtn   *gtk.Button
	collapseAllBtn *gtk.Button
	copyHashBtn    *gtk.Button
	headJumpBtn    *gtk.Button
	setUpstreamFixBtn *gtk.Button

	repoTitle   *gtk.Label
	repoPath    *gtk.Label
	infoLabel   *gtk.Label
	remoteLabel *gtk.Label
	branchLabel *gtk.Label
	aheadLabel  *gtk.Label
	statsLabel  *gtk.Label
	emptyLabel  *gtk.Label

	stack *gtk.Stack

	selectedRepoRow   *gtk.ListBoxRow
	selectedFileRow   *gtk.ListBoxRow
	selectedCommitRow *gtk.ListBoxRow

	selectedRepoPath string
	selectedFile     string
	selectedFileMode bool
	selectedCommit   string

	branchDropBound bool

	branchDropUpdating bool
	infoStickyUntil    time.Time

	conflictFiles    []ConflictFile
	conflictFileIdx  int
	conflictHunkIdx  int

	conflictFileList    *gtk.ListBox
	conflictOursBuf     *gtk.TextBuffer
	conflictBaseBuf     *gtk.TextBuffer
	conflictTheirsBuf   *gtk.TextBuffer
	conflictResBuf      *gtk.TextBuffer
	conflictResView     *gtk.TextView
	conflictStatusLbl   *gtk.Label
	conflictHunkLbl     *gtk.Label
	conflictContinueBtn *gtk.Button
	conflictDrafts map[string]string
	conflictLoading bool
	conflictPanelOpen bool
}

func NewApp(cfg Config) *App {
	a := &App{cfg: cfg}
	a.repos = DiscoverRepos(cfg.Repos.Paths, cfg.Behavior.ScanParentOnStart)
	a.conflictDrafts = make(map[string]string)
	return a
}

func (a *App) Run() {
	a.app = gtk.NewApplication("dev.timuzkas.gig", gio.ApplicationFlagsNone)
	a.app.ConnectActivate(a.build)
	a.app.Run(os.Args)
}

func (a *App) build() {
	settings := gtk.SettingsGetDefault()
    if settings != nil {
        settings.SetObjectProperty("gtk-application-prefer-dark-theme", true)
    }

	a.loadCSS()

	a.win = gtk.NewApplicationWindow(a.app)
	a.win.SetTitle("gig")
	a.win.AddCSSClass("main-window")
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

* { font-family: "%s"; font-size: %dpt; }

window { background-color: @bg; }
`,
		a.cfg.Colors.Bg, a.cfg.Colors.Surface, a.cfg.Colors.Surface2,
		a.cfg.Colors.Border, a.cfg.Colors.Text, a.cfg.Colors.TextDim,
		a.cfg.Colors.Accent, a.cfg.Colors.Added, a.cfg.Colors.Removed,
		a.cfg.Colors.Modified, a.cfg.Colors.Selection,
		a.cfg.Appearance.FontFamily, a.cfg.Appearance.FontSize,
	)

	cssVars += defaultCSS

	provider.LoadFromData(cssVars)
	gtk.StyleContextAddProviderForDisplay(display, provider, gtk.STYLE_PROVIDER_PRIORITY_USER)
}

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
	a.conflictPanelOpen = false
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
	closeBtn.AddCSSClass("overlay-close-btn")
	closeBtn.ConnectClicked(func() { a.hideOverlay() })
	hdr.Append(closeBtn)
	card.Append(hdr)

	wrap := gtk.NewBox(gtk.OrientationVertical, 0)
	wrap.SetVExpand(true)
	wrap.Append(content)
	card.Append(wrap)
	return card
}

func (a *App) gitErrorDialog(title, errMsg string) {
	content := gtk.NewBox(gtk.OrientationVertical, 16)
	content.SetMarginTop(20)
	content.SetMarginBottom(20)
	content.SetMarginStart(20)
	content.SetMarginEnd(20)

	intro := "An error occurred during git operation."
	var files []string
	
	if strings.Contains(errMsg, "overwritten by checkout") || strings.Contains(errMsg, "overwritten by merge") {
		intro = "Local changes would be overwritten. Please commit or stash them:"
		lines := strings.Split(errMsg, "\n")
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "error:") || strings.HasPrefix(l, "Please") || strings.HasPrefix(l, "Aborting") {
				continue
			}
			files = append(files, l)
		}
	}

	introLbl := gtk.NewLabel(intro)
	introLbl.SetXAlign(0)
	introLbl.SetWrap(true)
	introLbl.AddCSSClass("dim")
	content.Append(introLbl)

	if len(files) > 0 {
		fileList := gtk.NewBox(gtk.OrientationVertical, 4)
		fileList.SetMarginStart(12)
		for _, f := range files {
			row := gtk.NewBox(gtk.OrientationHorizontal, 8)
			icon := gtk.NewLabel("•")
			icon.AddCSSClass("status-removed")
			name := gtk.NewLabel(f)
			name.AddCSSClass("repo-name")
			row.Append(icon)
			row.Append(name)
			fileList.Append(row)
		}
		content.Append(fileList)
	} else {
		scroll := gtk.NewScrolledWindow()
		scroll.SetMinContentHeight(100)
		scroll.SetMaxContentHeight(300)
		
		errLbl := gtk.NewLabel(errMsg)
		errLbl.SetXAlign(0)
		errLbl.SetWrap(true)
		errLbl.SetSelectable(true)
		errLbl.AddCSSClass("status-removed")
		scroll.SetChild(errLbl)
		content.Append(scroll)
	}

	btnRow := gtk.NewBox(gtk.OrientationHorizontal, 12)
	btnRow.SetHAlign(gtk.AlignEnd)
	btnRow.SetMarginTop(10)

	stashBtn := gtk.NewButtonWithLabel("Stash & Continue")
	stashBtn.AddCSSClass("suggested-action")
	stashBtn.ConnectClicked(func() {
		a.hideOverlay()
		a.setInfo("Stashing changes…")
		go func() {
			err := StashSave(a.state.Path, "Auto-stash before checkout")
			glib.IdleAdd(func() {
				if err != nil {
					a.setInfoErr(err.Error())
					return
				}
				a.setInfoOk("Stashed. Retrying checkout…")
				a.doReload(true)
			})
		}()
	})

	closeBtn := gtk.NewButtonWithLabel("Close")
	closeBtn.ConnectClicked(func() { a.hideOverlay() })
	
	if len(files) > 0 {
		btnRow.Append(stashBtn)
	}
	btnRow.Append(closeBtn)
	content.Append(btnRow)

	card := a.buildOverlayCard(title, 480, content)
	a.showOverlay(card)
}

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

func (a *App) openConflictPanel() {
	if a.state == nil {
		return
	}
	a.conflictPanelOpen = true
	a.conflictFiles = GetConflictFiles(a.state.Path)
	if len(a.conflictFiles) == 0 {
		return
	}
	a.conflictFileIdx = -1
	a.conflictHunkIdx = 0
	a.conflictDrafts = make(map[string]string)

	card := gtk.NewBox(gtk.OrientationVertical, 0)
	card.AddCSSClass("overlay-panel")
	card.SetSizeRequest(1100, 700)

	hdr := gtk.NewBox(gtk.OrientationHorizontal, 10)
	hdr.AddCSSClass("overlay-header")

	titleLbl := gtk.NewLabel("MERGE CONFLICT")
	titleLbl.SetHExpand(true)
	titleLbl.SetXAlign(0)

	a.conflictStatusLbl = gtk.NewLabel("")
	a.conflictStatusLbl.AddCSSClass("dim")

	abortBtn := gtk.NewButtonWithLabel("Abort Merge")
	abortBtn.AddCSSClass("destructive-action")
	abortBtn.ConnectClicked(func() {
		a.confirmDialog("Abort Merge?",
			"This will undo the merge and restore your branch to its previous state.",
			true, func() {
				go func(repo string) {
					err := AbortMerge(repo)
					glib.IdleAdd(func() {
						a.hideOverlay()
						if err != nil {
							a.setInfoErr(err.Error())
						} else {
							a.setInfoOk("Merge aborted")
						}
						a.doReload(true)
					})
				}(a.state.Path)
			})
	})

	hdr.Append(titleLbl)
	hdr.Append(a.conflictStatusLbl)
	card.Append(hdr)

	body := gtk.NewPaned(gtk.OrientationHorizontal)

	leftBox := gtk.NewBox(gtk.OrientationVertical, 0)
	leftBox.SetSizeRequest(220, -1)

	fileHdr := gtk.NewLabel("CONFLICTED FILES")
	fileHdr.AddCSSClass("section-label")
	fileHdr.SetMarginTop(10)
	fileHdr.SetMarginBottom(6)
	fileHdr.SetMarginStart(14)
	fileHdr.SetXAlign(0)
	leftBox.Append(fileHdr)

	a.conflictFileList = gtk.NewListBox()
	a.conflictFileList.SetSelectionMode(gtk.SelectionSingle)
	a.conflictFileList.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if row == nil {
			return
		}

		if a.conflictResBuf != nil &&
			a.conflictFileIdx >= 0 &&
			a.conflictFileIdx < len(a.conflictFiles) {
			a.saveCurrentConflictDraft()
		}

		a.conflictFileIdx = row.Index()
		a.conflictHunkIdx = 0
		a.loadConflictFile()
	})

	fileScroll := gtk.NewScrolledWindow()
	fileScroll.SetVExpand(true)
	fileScroll.SetChild(a.conflictFileList)
	leftBox.Append(fileScroll)

	bulkLabel := gtk.NewLabel("BULK ACTIONS")
	bulkLabel.AddCSSClass("section-label")
	bulkLabel.SetMarginTop(10)
	bulkLabel.SetMarginStart(14)
	bulkLabel.SetXAlign(0)
	leftBox.Append(bulkLabel)

	bulkBox := gtk.NewBox(gtk.OrientationHorizontal, 6)
	bulkBox.AddCSSClass("conflict-bulk-actions")
	bulkBox.SetHExpand(true)

	takeAllOursBtn := gtk.NewButtonWithLabel("Ours")
	takeAllOursBtn.SetTooltipText("Take ALL ours for this file")
	takeAllOursBtn.SetHExpand(true)
	takeAllOursBtn.ConnectClicked(func() {
		a.confirmDialog("Take All Ours?",
			"Accept our version for all conflicts in this file?",
			false, func() { a.resolveFileWith(ResolutionOurs) })
	})

	takeAllTheirsBtn := gtk.NewButtonWithLabel("Theirs")
	takeAllTheirsBtn.SetTooltipText("Take ALL theirs for this file")
	takeAllTheirsBtn.SetHExpand(true)
	takeAllTheirsBtn.ConnectClicked(func() {
		a.confirmDialog("Take All Theirs?",
			"Accept their version for all conflicts in this file?",
			false, func() { a.resolveFileWith(ResolutionTheirs) })
	})
	bulkBox.Append(takeAllOursBtn)
	bulkBox.Append(takeAllTheirsBtn)
	leftBox.Append(bulkBox)

	body.SetStartChild(leftBox)
	body.SetResizeStartChild(false)

	rightBox := a.buildConflictEditor()
	body.SetEndChild(rightBox)
	body.SetPosition(220)
	card.Append(body)

	footer := gtk.NewBox(gtk.OrientationHorizontal, 8)
	footer.AddCSSClass("overlay-header")
	footer.SetMarginTop(0)

	footer.Append(abortBtn)

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	footer.Append(spacer)

	a.conflictContinueBtn = gtk.NewButtonWithLabel("Continue Merge")
	a.conflictContinueBtn.AddCSSClass("suggested-action")
	a.conflictContinueBtn.SetSensitive(false)
	a.conflictContinueBtn.ConnectClicked(func() {
		go func(repo string) {
			err := ContinueMerge(repo)
			glib.IdleAdd(func() {
				a.hideOverlay()
				if err != nil {
					a.setInfoErr(err.Error())
				} else {
					a.setInfoOk("Merge complete")
				}
				a.doReload(true)
			})
		}(a.state.Path)
	})
	footer.Append(a.conflictContinueBtn)
	card.Append(footer)

	a.populateConflictFileList()
	// Row selection triggers loading, so we don't call loadConflictFile manually if row 0 is selected
	if a.conflictFileIdx == -1 && len(a.conflictFiles) > 0 {
		a.conflictFileIdx = 0
		a.loadConflictFile()
	}
	a.showOverlay(card)
}

func (a *App) saveCurrentConflictDraft() {
	if a.conflictLoading || a.conflictResBuf == nil ||
		a.conflictFileIdx >= len(a.conflictFiles) {
		return
	}

	path := a.conflictFiles[a.conflictFileIdx].Path
	text := a.conflictResBuf.Text(
		a.conflictResBuf.StartIter(),
		a.conflictResBuf.EndIter(),
		false,
	)
	a.conflictDrafts[path] = text
}

func (a *App) getConflictDraft(path string, fallback string) string {
	if text, ok := a.conflictDrafts[path]; ok {
		return text
	}
	return fallback
}

func (a *App) buildConflictEditor() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.SetVExpand(true)
	box.SetHExpand(true)

	mainPaned := gtk.NewPaned(gtk.OrientationVertical)
	mainPaned.SetVExpand(true)
	mainPaned.SetHExpand(true)

	topPaned := gtk.NewPaned(gtk.OrientationHorizontal)
	topPaned.SetVExpand(true)
	topPaned.SetHExpand(true)

	oursBox := a.buildConflictPane(
		"OURS",
		"conflict-ours",
		&a.conflictOursBuf,
	)
	theirsBox := a.buildConflictPane(
		"THEIRS",
		"conflict-theirs",
		&a.conflictTheirsBuf,
	)

	a.conflictBaseBuf = gtk.NewTextBuffer(nil)

	topPaned.SetStartChild(oursBox)
	topPaned.SetEndChild(theirsBox)
	topPaned.SetResizeStartChild(true)
	topPaned.SetResizeEndChild(true)
	topPaned.SetShrinkStartChild(true)
	topPaned.SetShrinkEndChild(true)
	topPaned.SetPosition(470)

	resHeader := gtk.NewBox(gtk.OrientationHorizontal, 6)
	resHeader.AddCSSClass("conflict-pane-header")

	resLbl := gtk.NewLabel("RESOLUTION")
	resLbl.AddCSSClass("conflict-pane-label")
	resLbl.AddCSSClass("conflict-pane-label-resolution")
	resLbl.SetXAlign(0)
	resLbl.SetHExpand(true)
	resHeader.Append(resLbl)

	a.conflictResBuf = gtk.NewTextBuffer(nil)
	a.conflictResBuf.ConnectChanged(func() {
		a.saveCurrentConflictDraft()
	})

	a.conflictResView = gtk.NewTextViewWithBuffer(a.conflictResBuf)
	a.conflictResView.SetEditable(true)
	a.conflictResView.SetMonospace(true)
	a.conflictResView.SetLeftMargin(12)
	a.conflictResView.SetRightMargin(12)
	a.conflictResView.SetTopMargin(8)
	a.conflictResView.SetBottomMargin(8)
	a.conflictResView.SetWrapMode(gtk.WrapNone)
	a.conflictResView.SetVExpand(true)
	a.conflictResView.SetHExpand(true)
	a.conflictResView.AddCSSClass("conflict-res-view")

	resScroll := gtk.NewScrolledWindow()
	resScroll.SetVExpand(true)
	resScroll.SetHExpand(true)
	resScroll.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	resScroll.SetChild(a.conflictResView)

	resBox := gtk.NewBox(gtk.OrientationVertical, 0)
	resBox.SetVExpand(true)
	resBox.SetHExpand(true)
	resBox.AddCSSClass("conflict-pane")
	resBox.AddCSSClass("conflict-pane-res")
	resBox.Append(resHeader)
	resBox.Append(resScroll)

	mainPaned.SetStartChild(topPaned)
	mainPaned.SetEndChild(resBox)
	mainPaned.SetResizeStartChild(true)
	mainPaned.SetResizeEndChild(true)
	mainPaned.SetShrinkStartChild(true)
	mainPaned.SetShrinkEndChild(false)
	mainPaned.SetPosition(240)

	navBar := gtk.NewBox(gtk.OrientationHorizontal, 6)
	navBar.AddCSSClass("conflict-nav-bar")

	prevBtn := gtk.NewButtonWithLabel("← Prev Hunk")
	prevBtn.AddCSSClass("flat")
	prevBtn.ConnectClicked(func() { a.navigateHunk(-1) })

	a.conflictHunkLbl = gtk.NewLabel("Hunk 0 / 0")
	a.conflictHunkLbl.AddCSSClass("dim")

	nextBtn := gtk.NewButtonWithLabel("Next Hunk →")
	nextBtn.AddCSSClass("flat")
	nextBtn.ConnectClicked(func() { a.navigateHunk(1) })

	navSpacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	navSpacer.SetHExpand(true)

	useOursBtn := gtk.NewButtonWithLabel("Use Ours")
	useTheirsBtn := gtk.NewButtonWithLabel("Use Theirs")
	useBothBtn := gtk.NewButtonWithLabel("Use Both")
	doneBtn := gtk.NewButtonWithLabel("✓ Accept Resolution")
	doneBtn.AddCSSClass("suggested-action")

	useOursBtn.ConnectClicked(func() { a.applyHunkResolution(ResolutionOurs) })
	useTheirsBtn.ConnectClicked(func() { a.applyHunkResolution(ResolutionTheirs) })
	useBothBtn.ConnectClicked(func() { a.applyHunkResolution(ResolutionBoth) })
	doneBtn.ConnectClicked(func() { a.acceptCurrentHunk() })

	navBar.Append(prevBtn)
	navBar.Append(a.conflictHunkLbl)
	navBar.Append(nextBtn)
	navBar.Append(navSpacer)
	navBar.Append(useOursBtn)
	navBar.Append(useTheirsBtn)
	navBar.Append(useBothBtn)
	navBar.Append(doneBtn)

	box.Append(mainPaned)
	box.Append(navBar)
	return box
}

func (a *App) buildConflictPane(title, cssClass string, buf **gtk.TextBuffer) *gtk.Box {
	hdr := gtk.NewBox(gtk.OrientationHorizontal, 0)
	hdr.AddCSSClass("conflict-pane-header")
	lbl := gtk.NewLabel(title)
	lbl.SetXAlign(0)
	lbl.AddCSSClass("conflict-pane-label")
	lbl.AddCSSClass(cssClass + "-label")
	hdr.Append(lbl)

	*buf = gtk.NewTextBuffer(nil)
	tv := gtk.NewTextViewWithBuffer(*buf)
	tv.SetEditable(false)
	tv.SetMonospace(true)
	tv.SetLeftMargin(12)
	tv.AddCSSClass("conflict-view")
	tv.AddCSSClass(cssClass + "-view")

	scroll := gtk.NewScrolledWindow()
	scroll.SetVExpand(true)
	scroll.SetChild(tv)

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("conflict-pane")
	box.AddCSSClass(cssClass)
	box.Append(hdr)
	box.Append(scroll)
	return box
}

func (a *App) populateConflictFileList() {
	clearListBox(a.conflictFileList)
	for i, f := range a.conflictFiles {
		row := gtk.NewListBoxRow()
		box := gtk.NewBox(gtk.OrientationHorizontal, 8)
		box.SetMarginStart(10)
		box.SetMarginEnd(10)
		box.SetMarginTop(6)
		box.SetMarginBottom(6)

		icon := "●"
		if f.Resolved {
			icon = "✓"
		}
		iconLbl := gtk.NewLabel(icon)
		if f.Resolved {
			iconLbl.AddCSSClass("info-ok")
		} else {
			iconLbl.AddCSSClass("status-removed")
		}

		name := gtk.NewLabel(f.Path)
		name.SetHExpand(true)
		name.SetXAlign(0)
		name.SetEllipsize(3)

		box.Append(iconLbl)
		box.Append(name)
		row.SetChild(box)
		a.conflictFileList.Append(row)

		if i == a.conflictFileIdx {
			a.conflictFileList.SelectRow(row)
		}
	}
}

func (a *App) loadConflictFile() {
	if a.conflictFileIdx >= len(a.conflictFiles) {
		return
	}
	cf := &a.conflictFiles[a.conflictFileIdx]

	base, ours, theirs := GetFileVersions(a.state.Path, cf.Path)

	content, _ := os.ReadFile(filepath.Join(a.state.Path, cf.Path))
	currentText := a.getConflictDraft(cf.Path, string(content))
	cf.Hunks = ParseConflictHunks(currentText)

	a.conflictLoading = true
	a.conflictOursBuf.SetText(ours)
	a.conflictBaseBuf.SetText(base)
	a.conflictTheirsBuf.SetText(theirs)
	a.conflictResBuf.SetText(currentText)
	a.conflictLoading = false

	a.updateHunkNav()
	a.scrollToCurrentHunk()
}

func (a *App) updateHunkNav() {
	cf := &a.conflictFiles[a.conflictFileIdx]
	total := len(cf.Hunks)
	current := a.conflictHunkIdx + 1
	if total == 0 {
		current = 0
	}
	a.conflictHunkLbl.SetText(fmt.Sprintf("Hunk %d / %d", current, total))
	a.updateConflictFooter()
}

func (a *App) navigateHunk(delta int) {
	cf := &a.conflictFiles[a.conflictFileIdx]
	if len(cf.Hunks) == 0 {
		return
	}
	a.conflictHunkIdx += delta
	if a.conflictHunkIdx < 0 {
		a.conflictHunkIdx = 0
	}
	if a.conflictHunkIdx >= len(cf.Hunks) {
		a.conflictHunkIdx = len(cf.Hunks) - 1
	}
	a.updateHunkNav()
	a.scrollToCurrentHunk()
}

func (a *App) scrollToCurrentHunk() {
	cf := &a.conflictFiles[a.conflictFileIdx]
	if a.conflictHunkIdx >= len(cf.Hunks) {
		return
	}
	hunk := cf.Hunks[a.conflictHunkIdx]
	iter, _ := a.conflictResBuf.IterAtLine(hunk.StartLine)
	a.conflictResView.ScrollToIter(iter, 0.1, false, 0, 0)
}

func (a *App) applyHunkResolution(r HunkResolution) {
	cf := &a.conflictFiles[a.conflictFileIdx]
	if a.conflictHunkIdx >= len(cf.Hunks) {
		return
	}

	hunk := &cf.Hunks[a.conflictHunkIdx]
	hunk.Resolution = r

	var lines []string
	switch r {
	case ResolutionOurs:
		lines = hunk.OursLines
	case ResolutionTheirs:
		lines = hunk.TheirsLines
	case ResolutionBoth:
		lines = append(hunk.OursLines, hunk.TheirsLines...)
	case ResolutionBothRev:
		lines = append(hunk.TheirsLines, hunk.OursLines...)
	}

	currentIdx := a.conflictHunkIdx
	a.spliceResolutionBuffer(hunk, lines)

	text := a.getConflictDraft(
		cf.Path,
		a.conflictResBuf.Text(
			a.conflictResBuf.StartIter(),
			a.conflictResBuf.EndIter(),
			false,
		),
	)

	cf.Hunks = ParseConflictHunks(text)

	if len(cf.Hunks) == 0 {
		a.conflictHunkIdx = 0
		a.updateHunkNav()
		return
	}

	if currentIdx >= len(cf.Hunks) {
		a.conflictHunkIdx = len(cf.Hunks) - 1
	} else {
		a.conflictHunkIdx = currentIdx
	}

	a.updateHunkNav()
	a.scrollToCurrentHunk()
}

func (a *App) spliceResolutionBuffer(hunk *ConflictHunk, lines []string) {
	text := a.conflictResBuf.Text(
		a.conflictResBuf.StartIter(),
		a.conflictResBuf.EndIter(),
		false,
	)
	allLines := strings.Split(text, "\n")

	start := -1
	end := -1
	count := 0

	for i, line := range allLines {
		if strings.HasPrefix(line, "<<<<<<< ") {
			if count == a.conflictHunkIdx {
				start = i
			}
		}
		if strings.HasPrefix(line, ">>>>>>> ") {
			if count == a.conflictHunkIdx {
				end = i
				break
			}
			count++
		}
	}

	if start == -1 || end == -1 {
		return
	}

	repl := append([]string{}, allLines[:start]...)
	repl = append(repl, lines...)
	repl = append(repl, allLines[end+1:]...)

	newText := strings.Join(repl, "\n")
	a.conflictLoading = true
	a.conflictResBuf.SetText(newText)
	a.conflictLoading = false
	a.saveCurrentConflictDraft()
}

func (a *App) acceptCurrentHunk() {
	cf := &a.conflictFiles[a.conflictFileIdx]
	
	// If there are hunks, mark the current one as resolved
	if len(cf.Hunks) > 0 && a.conflictHunkIdx < len(cf.Hunks) {
		hunk := &cf.Hunks[a.conflictHunkIdx]
		if hunk.Resolution == ResolutionNone {
			hunk.Resolution = ResolutionCustom
		}
	}

	// Check if all markers are gone (either by parsing or by manual edit)
	text := a.conflictResBuf.Text(a.conflictResBuf.StartIter(), a.conflictResBuf.EndIter(), false)
	remainingHunks := ParseConflictHunks(text)
	
	if len(remainingHunks) == 0 {
		a.markFileResolved(a.conflictFileIdx)
	} else {
		// If we still have hunks, try to move to the next one
		a.navigateHunk(1)
	}
}

func (a *App) resolveFileWith(r HunkResolution) {
	cf := &a.conflictFiles[a.conflictFileIdx]
	for i := range cf.Hunks {
		a.conflictHunkIdx = i
		hunk := &cf.Hunks[i]
		if hunk.Resolution == ResolutionNone {
			var lines []string
			switch r {
			case ResolutionOurs: lines = hunk.OursLines
			case ResolutionTheirs: lines = hunk.TheirsLines
			}
			a.spliceResolutionBuffer(hunk, lines)
			hunk.Resolution = r
		}
	}
	a.markFileResolved(a.conflictFileIdx)
}

func (a *App) markFileResolved(idx int) {
	cf := &a.conflictFiles[idx]

	text := a.getConflictDraft(
		cf.Path,
		a.conflictResBuf.Text(
			a.conflictResBuf.StartIter(),
			a.conflictResBuf.EndIter(),
			false,
		),
	)
	_ = os.WriteFile(filepath.Join(a.state.Path, cf.Path), []byte(text), 0644)

	go func(repo, path string) {
		_ = MarkResolved(repo, path)
		glib.IdleAdd(func() {
			cf.Resolved = true
			a.populateConflictFileList()
			a.updateConflictFooter()
			a.jumpToNextConflictFile()
		})
	}(a.state.Path, cf.Path)
}

func (a *App) jumpToNextConflictFile() {
	for i, f := range a.conflictFiles {
		if !f.Resolved {
			a.conflictFileIdx = i
			a.conflictHunkIdx = 0
			a.loadConflictFile()
			return
		}
	}
}

func (a *App) updateConflictFooter() {
	total := len(a.conflictFiles)
	resolved := 0
	for _, f := range a.conflictFiles {
		if f.Resolved {
			resolved++
		}
	}

	allDone := resolved == total
	a.conflictContinueBtn.SetSensitive(allDone)
	a.conflictContinueBtn.SetLabel(fmt.Sprintf("Continue Merge (%d/%d resolved)", resolved, total))
	a.conflictStatusLbl.SetText(fmt.Sprintf("%d files · %d resolved", total, resolved))
}

func (a *App) buildHeader() *gtk.HeaderBar {
	hdr := gtk.NewHeaderBar()
	hdr.SetShowTitleButtons(true)

	left := gtk.NewBox(gtk.OrientationHorizontal, 5)

	a.branchDrop = gtk.NewDropDown(nil, nil)
	a.branchDrop.SetSizeRequest(190, -1)
	a.branchDrop.SetHExpand(false)
	a.branchDrop.SetHAlign(gtk.AlignStart)
	a.branchDrop.SetVAlign(gtk.AlignCenter)
	left.Append(a.branchDrop)

	a.headJumpBtn = gtk.NewButtonWithLabel("")
	a.headJumpBtn.AddCSSClass("head-jump-btn")
	a.headJumpBtn.SetVisible(false)
	a.headJumpBtn.SetTooltipText("Not at HEAD — click to jump back")
	a.headJumpBtn.ConnectClicked(func() { a.jumpToHEAD() })
	left.Append(a.headJumpBtn)

	for _, def := range []struct {
		label, tooltip string
		fn             func()
	}{
		{"↓ Fetch", "Fetch all remotes", func() { a.runGitOp("Fetching…", "Fetch complete", Fetch) }},
		{"⇓ Pull", "Pull current branch", func() { a.runGitOpSafe("Pulling…", "Pull complete", Pull) }},
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

func (a *App) runGitOp(startMsg, okMsg string, fn func(string) error) {
	if a.state == nil {
		return
	}
	a.setInfo(startMsg)
	go func(repo string) {
		err := fn(repo)
		glib.IdleAdd(func() {
			if err != nil {
				if IsConflicted(repo) {
					a.setInfoErr("Merge conflict!")
					a.openConflictPanel()
				} else {
					a.setInfoErr(strings.TrimPrefix(err.Error(), "exit status 1: "))
				}
			} else {
				a.setInfoOk(okMsg)
			}
			a.doReload(true)
		})
	}(a.state.Path)
}

func (a *App) runGitOpSafe(startMsg, okMsg string, fn func(string) error) {
	if a.state == nil {
		return
	}
	doIt := func() {
		a.runGitOp(startMsg, okMsg, fn)
	}
	if a.hasUncommittedChanges() && !IsConflicted(a.state.Path) {
		a.confirmDialog(
			"Uncommitted Changes",
			"You have uncommitted or staged changes. Continue anyway? (they may be lost)",
			true,
			doIt,
		)
		return
	}
	doIt()
}

func (a *App) jumpToHEAD() {
	if a.state == nil {
		return
	}
	if a.hasUncommittedChanges() {
		a.confirmDialog(
			"Uncommitted Changes",
			"You have uncommitted changes. Stash them before switching to HEAD?",
			false,
			func() {
				go func(repo string) {
					_ = StashSave(repo, "Auto-stash before HEAD checkout")
					glib.IdleAdd(func() { a.doCheckoutHEAD() })
				}(a.state.Path)
			},
		)
		return
	}
	a.doCheckoutHEAD()
}

func (a *App) doCheckoutHEAD() {
	a.setInfo("Jumping to HEAD…")
	go func(repo string) {
		err := Checkout(repo, "HEAD")
		if err != nil {
			err = gitCmd2(repo, "checkout", "-")
		}
		glib.IdleAdd(func() {
			if err != nil {
				a.setInfoErr(err.Error())
			} else {
				a.setInfoOk("At HEAD")
			}
			a.doReload(true)
		})
	}(a.state.Path)
}

func (a *App) hasUncommittedChanges() bool {
	if a.state == nil {
		return false
	}
	for _, f := range a.state.Files {
		if f.Staged || f.WorkStatus != " " {
			return true
		}
	}
	return false
}

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

func (a *App) buildFileSidebar() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("file-sidebar")

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

func (a *App) buildContentArea() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("content-panel")

	infoBar := gtk.NewBox(gtk.OrientationHorizontal, 10)
	infoBar.AddCSSClass("top-info-bar")
	infoBar.SetMarginTop(7)
	infoBar.SetMarginBottom(7)
	infoBar.SetMarginStart(0)
	infoBar.SetMarginEnd(0)

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
	a.infoLabel.SetEllipsize(3)
	a.infoLabel.SetMaxWidthChars(60)
	infoBar.Append(a.infoLabel)

	box.Append(infoBar)

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

	diffContainer := gtk.NewBox(gtk.OrientationVertical, 0)
	diffContainer.AddCSSClass("diff-area")

	diffToolbar := gtk.NewBox(gtk.OrientationHorizontal, 0)
	diffToolbar.AddCSSClass("diff-toolbar")

	a.splitToggleBtn = gtk.NewButtonWithLabel("Show Split")
	a.splitToggleBtn.AddCSSClass("flat")
	a.splitToggleBtn.AddCSSClass("diff-toggle-btn")
	a.splitToggleBtn.SetHAlign(gtk.AlignStart)
	a.splitToggleBtn.ConnectClicked(func() {
		a.showSplit = !a.showSplit
		if a.showSplit {
			a.splitToggleBtn.SetLabel("Show Unified")
			a.expandAllBtn.SetVisible(false)
			a.collapseAllBtn.SetVisible(false)
		} else {
			a.splitToggleBtn.SetLabel("Show Split")
			a.expandAllBtn.SetVisible(true)
			a.collapseAllBtn.SetVisible(true)
		}
		if a.selectedCommit != "" {
			a.loadCommitDiff(a.selectedCommit)
		} else if a.selectedFile != "" {
			a.loadFileDiff(a.selectedFile, a.selectedFileMode)
		}
	})
	diffToolbar.Append(a.splitToggleBtn)

	a.expandAllBtn = gtk.NewButtonWithLabel("Expand All")
	a.expandAllBtn.AddCSSClass("flat")
	a.expandAllBtn.AddCSSClass("diff-toggle-btn")
	a.expandAllBtn.ConnectClicked(func() { a.toggleAllDiffs(true) })
	diffToolbar.Append(a.expandAllBtn)

	a.collapseAllBtn = gtk.NewButtonWithLabel("Collapse All")
	a.collapseAllBtn.AddCSSClass("flat")
	a.collapseAllBtn.AddCSSClass("diff-toggle-btn")
	a.collapseAllBtn.ConnectClicked(func() { a.toggleAllDiffs(false) })
	diffToolbar.Append(a.collapseAllBtn)

	a.copyHashBtn = gtk.NewButtonWithLabel("Copy Hash")
	a.copyHashBtn.AddCSSClass("flat")
	a.copyHashBtn.AddCSSClass("diff-toggle-btn")
	a.copyHashBtn.SetVisible(false)
	a.copyHashBtn.ConnectClicked(func() {
		if a.selectedCommit != "" {
			clipboard := a.win.Clipboard()
			clipboard.SetText(a.selectedCommit)
			a.setInfoOk("Copied " + a.selectedCommit[:8])
		}
	})
	diffToolbar.Append(a.copyHashBtn)

	diffContainer.Append(diffToolbar)

	a.commitHeader = gtk.NewBox(gtk.OrientationVertical, 0)
	a.commitHeader.SetVisible(false)
	a.commitHeader.AddCSSClass("commit-details-table")

	row1 := gtk.NewBox(gtk.OrientationHorizontal, 0)
	row1.AddCSSClass("commit-details-row")
	a.commitAuthor = gtk.NewLabel("")
	a.commitAuthor.SetXAlign(0)
	a.commitAuthor.AddCSSClass("commit-details-author")
	row1.Append(a.commitAuthor)

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	row1.Append(spacer)

	a.commitHashFull = gtk.NewLabel("")
	a.commitHashFull.SetXAlign(1)
	a.commitHashFull.AddCSSClass("commit-details-hash")
	row1.Append(a.commitHashFull)
	a.commitHeader.Append(row1)
	a.commitHeader.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	row2 := gtk.NewBox(gtk.OrientationHorizontal, 0)
	row2.AddCSSClass("commit-details-row")
	a.commitDateFull = gtk.NewLabel("")
	a.commitDateFull.SetXAlign(0)
	a.commitDateFull.AddCSSClass("commit-details-date")
	row2.Append(a.commitDateFull)
	a.commitHeader.Append(row2)
	a.commitHeader.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	row3 := gtk.NewBox(gtk.OrientationHorizontal, 0)
	row3.AddCSSClass("commit-details-row")
	a.commitSubjectBold = gtk.NewLabel("")
	a.commitSubjectBold.SetXAlign(0)
	a.commitSubjectBold.SetWrap(true)
	a.commitSubjectBold.AddCSSClass("commit-details-subject")
	row3.Append(a.commitSubjectBold)
	a.commitHeader.Append(row3)
	a.commitHeader.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	diffContainer.Append(a.commitHeader)

	a.diffBuf = gtk.NewTextBuffer(nil)
	a.diffView = gtk.NewBox(gtk.OrientationVertical, 0)
	a.diffView.SetHExpand(true)
	a.diffView.AddCSSClass("diff-view-container")
	diffContainer.Append(a.diffView)

	a.splitView = gtk.NewBox(gtk.OrientationHorizontal, 0)
	a.splitView.SetVisible(false)
	a.splitView.SetHExpand(true)

	a.diffBufLeft = gtk.NewTextBuffer(nil)
	a.diffViewLeft = gtk.NewTextViewWithBuffer(a.diffBufLeft)
	a.diffViewLeft.SetEditable(false)
	a.diffViewLeft.SetMonospace(true)
	a.diffViewLeft.SetHExpand(true)
	a.diffViewLeft.AddCSSClass("diff-view-split")
	a.splitView.Append(a.diffViewLeft)

	vsep := gtk.NewSeparator(gtk.OrientationVertical)
	a.splitView.Append(vsep)

	a.diffBufRight = gtk.NewTextBuffer(nil)
	a.diffViewRight = gtk.NewTextViewWithBuffer(a.diffBufRight)
	a.diffViewRight.SetEditable(false)
	a.diffViewRight.SetMonospace(true)
	a.diffViewRight.SetHExpand(true)
	a.diffViewRight.AddCSSClass("diff-view-split")
	a.splitView.Append(a.diffViewRight)

	diffContainer.Append(a.splitView)

	a.diffScroll = gtk.NewScrolledWindow()
	a.diffScroll.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	a.diffScroll.SetVExpand(true)
	a.diffScroll.SetChild(diffContainer)

	paned.SetStartChild(logScroll)
	paned.SetEndChild(a.diffScroll)
	paned.SetPosition(300)
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

func (a *App) buildStatusStrip() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.AddCSSClass("status-strip")

	a.branchLabel = gtk.NewLabel("")
	a.branchLabel.SetMarginEnd(8)

	a.aheadLabel = gtk.NewLabel("")
	a.aheadLabel.AddCSSClass("ahead-behind")
	a.aheadLabel.SetMarginEnd(8)

	a.statsLabel = gtk.NewLabel("")

	a.setUpstreamFixBtn = gtk.NewButtonWithLabel("Fix?")
	a.setUpstreamFixBtn.AddCSSClass("flat")
	a.setUpstreamFixBtn.AddCSSClass("head-jump-btn")
	a.setUpstreamFixBtn.AddCSSClass("fix-upstream-btn")
	a.setUpstreamFixBtn.SetTooltipText("Set upstream tracking for this branch")
	a.setUpstreamFixBtn.SetVisible(false)
	a.setUpstreamFixBtn.ConnectClicked(func() { a.openSetUpstreamDialog() })

	box.Append(a.branchLabel)
	box.Append(a.aheadLabel)
	box.Append(a.setUpstreamFixBtn)
	box.Append(a.statsLabel)

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	box.Append(spacer)
	return box
}

func (a *App) setInfo(t string) {
	if a.infoLabel == nil {
		return
	}
	clean := strings.ReplaceAll(t, "\n", " ")
	a.infoLabel.SetText(clean)
	a.infoLabel.SetTooltipText(t)
	a.infoLabel.RemoveCSSClass("info-ok")
	a.infoLabel.RemoveCSSClass("info-err")
	a.infoLabel.AddCSSClass("dim")
	a.infoStickyUntil = time.Now().Add(5 * time.Second)
}

func (a *App) setInfoOk(t string) {
	if a.infoLabel == nil {
		return
	}
	clean := strings.ReplaceAll(t, "\n", " ")
	a.infoLabel.SetText(clean)
	a.infoLabel.SetTooltipText(t)
	a.infoLabel.RemoveCSSClass("dim")
	a.infoLabel.RemoveCSSClass("info-err")
	a.infoLabel.AddCSSClass("info-ok")
	a.infoStickyUntil = time.Now().Add(5 * time.Second)
}

func (a *App) setInfoErr(t string) {
	if a.infoLabel == nil {
		return
	}
	clean := strings.ReplaceAll(t, "\n", " ")
	a.infoLabel.SetText(clean)
	a.infoLabel.SetTooltipText(t)
	a.infoLabel.RemoveCSSClass("dim")
	a.infoLabel.RemoveCSSClass("info-ok")
	a.infoLabel.AddCSSClass("info-err")
	a.infoStickyUntil = time.Now().Add(10 * time.Second)
}

func (a *App) doReload(showDefault bool) {
	if a.cfg.Features.AsyncStateReload {
		a.reloadStateAsync(showDefault)
	} else {
		a.reloadState(showDefault)
	}
}

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
	EnsureDiff3Style(path)
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
		a.populateBranches()
	}

	a.syncBranchDrop()
	
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

	if IsConflicted(a.state.Path) {
		if !a.conflictPanelOpen {
			a.openConflictPanel()
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

func (a *App) bindBranchDrop() {
    if a.branchDropBound {
        return
    }
    a.branchDropBound = true

    a.branchDrop.Connect("notify::selected", func() {
        if a.branchDropUpdating || a.state == nil {
            return
        }

        sel := a.branchDrop.Selected()
        if sel == gtk.InvalidListPosition {
			return
		}

        modelObj := a.branchDrop.Model()
        if modelObj == nil {
            return
        }
        
        stringList := modelObj.Cast().(*gtk.StringList)
        target := stringList.String(sel)

        if target == "" || target == a.state.Branch {
            return
        }

        a.runGitOpSafe("Switching to "+target+"…", "On "+target, func(repo string) error {
            return Checkout(repo, target)
        })
    })
}

func (a *App) syncBranchDrop() {
    if a.state == nil {
        return
    }

    var localNames []string
    var curIdx uint = 0
    found := false

    for _, b := range a.state.Branches {
        if !b.IsRemote {
            if b.Name == a.state.Branch {
                curIdx = uint(len(localNames))
                found = true
            }
            localNames = append(localNames, b.Name)
        }
    }

    a.branchDropUpdating = true
    
    shouldUpdateModel := true
    if currentModel := a.branchDrop.Model(); currentModel != nil {
        sl := currentModel.Cast().(*gtk.StringList)
        if sl.NItems() == uint(len(localNames)) {
            match := true
            for i, name := range localNames {
                if sl.String(uint(i)) != name {
                    match = false
                    break
                }
            }
            if match {
                shouldUpdateModel = false
            }
        }
    }

    if shouldUpdateModel {
        a.branchDrop.SetModel(gtk.NewStringList(localNames))
    }

    if found {
        a.branchDrop.SetSelected(curIdx)
    }

    glib.IdleAdd(func() {
        a.branchDropUpdating = false
    })
    
    a.bindBranchDrop()
}

func (a *App) populateFiles() {
	clearListBox(a.fileListBox)
	a.selectedFileRow = nil

	if a.state == nil {
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

	ignored := GetIgnoredFiles(a.state.Path)
	if len(ignored) > 0 {
		a.appendIgnoredSection(ignored)
	}
}

func (a *App) appendIgnoredSection(files []string) {
	expander := gtk.NewExpander("Ignored Files")
	expander.AddCSSClass("ignored-expander")
	
	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	
	for _, f := range files {
		f := f
		row := gtk.NewListBoxRow()
		box := gtk.NewBox(gtk.OrientationHorizontal, 6)
		box.AddCSSClass("file-row-box")
		
		lbl := gtk.NewLabel(f)
		lbl.SetHExpand(true)
		lbl.SetXAlign(0)
		lbl.AddCSSClass("dim")
		
		unignoreBtn := gtk.NewButtonWithLabel("Un-ignore")
		unignoreBtn.AddCSSClass("flat")
		unignoreBtn.ConnectClicked(func() {
			UnignoreFile(a.state.Path, f)
			a.doReload(false)
		})
		
		box.Append(lbl)
		box.Append(unignoreBtn)
		row.SetChild(box)
		list.Append(row)
	}
	
	expander.SetChild(list)
	row := gtk.NewListBoxRow()
	row.SetSelectable(false)
	row.SetChild(expander)
	a.fileListBox.Append(row)
}

func (a *App) showFileContextMenu(relativeTo gtk.Widgetter, f FileStatus, staged bool) {
	menu := gtk.NewPopover()
	menu.SetHasArrow(true)
	menu.SetParent(relativeTo)

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	
	addItem := func(label string, destructive bool, fn func()) {
		btn := gtk.NewButtonWithLabel(label)
		btn.AddCSSClass("flat")
		if destructive { btn.AddCSSClass("destructive-action") }
		btn.ConnectClicked(func() {
			menu.Popdown()
			fn()
		})
		box.Append(btn)
	}

	addItem("Open in Editor", false, func() {
		OpenInEditor(a.state.Path, f.Path, a.cfg.Behavior.EditorCommand)
	})
	
	if staged {
		addItem("Unstage File", false, func() {
			UnstageFile(a.state.Path, f.Path)
			a.doReload(false)
		})
	} else {
		addItem("Stage File", false, func() {
			StageFile(a.state.Path, f.Path)
			a.doReload(false)
		})
	}

	addItem("Diff Changes", false, func() {
		a.loadFileDiff(f.Path, staged)
	})

	addItem("Stash this file", false, func() {
		StashSingleFile(a.state.Path, f.Path)
		a.doReload(false)
	})

	addItem("Add to .gitignore", false, func() {
		IgnoreFile(a.state.Path, f.Path)
		a.doReload(false)
	})

	addItem("REVERT (Destructive)", true, func() {
		a.confirmDialog("Revert File?", "Discard all changes in "+f.Path+"?", true, func() {
			RevertFile(a.state.Path, f.Path)
			a.doReload(false)
		})
	})

	menu.SetChild(box)
	menu.Popup()
}

func (a *App) appendFileSection(title string, files []FileStatus, staged bool) {
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
		click.SetButton(0)
		click.ConnectReleased(func(n int, x, y float64) {
			if click.CurrentButton() == 3 {
		        a.showFileContextMenu(row, f, staged)
		    } else {
				if a.state == nil {
					return
				}
				a.selectedFile = f.Path
				a.selectedFileMode = staged
				a.selectedCommit = ""
				a.loadFileDiff(f.Path, staged)
				a.markSelectedRow(a.fileListBox, row)
			}
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

	var graphInfos []CommitGraphInfo
	if filter == "" {
		graphInfos = ComputeGraphInfo(a.state.Commits)
	}

	for i, c := range a.state.Commits {
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

		if filter == "" && i < len(graphInfos) {
			info := graphInfos[i]
			graphArea := gtk.NewDrawingArea()
			graphArea.SetContentWidth(int(float64(len(info.Lanes)+1) * 14.0))
			graphArea.SetDrawFunc(func(_ *gtk.DrawingArea, cr *cairo.Context, width, height int) {
				DrawGraph(cr, info, float64(width), float64(height))
			})
			box.Append(graphArea)
		} else {
			graph := gtk.NewLabel(c.Graph)
			graph.SetXAlign(0)
			graph.AddCSSClass("commit-graph")
			box.Append(graph)
		}

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

		rightClick := gtk.NewGestureClick()
		rightClick.SetButton(3)
		rightClick.ConnectReleased(func(_ int, x, y float64) {
			a.showCommitContextMenu(row, c)
		})
		row.AddController(rightClick)

		src := gtk.NewDragSource()
		src.ConnectDragBegin(func(_ gdk.Dragger) {
		})
		src.ConnectPrepare(func(_, _ float64) *gdk.ContentProvider {
			return gdk.NewContentProviderForValue(glib.NewValue(c.Hash))
		})
		row.AddController(src)

		target := gtk.NewDropTarget(glib.TypeString, gdk.ActionCopy)
		target.ConnectDrop(func(val *glib.Value, _, _ float64) bool {
			srcHash := val.String()
			if srcHash == "" || srcHash == c.Hash {
				return false
			}
			a.showRebaseConfirmPopover(row, srcHash, c.Hash)
			return true
		})
		row.AddController(target)

		a.commitListBox.Append(row)
		count++
	}

	if count == 0 && filter != "" {
		a.commitListBox.Append(a.makePlaceholderRow("No commits match"))
	}
}

func (a *App) showRebaseConfirmPopover(relativeTo gtk.Widgetter, srcHash, destHash string) {
	pop := gtk.NewPopover()
	pop.SetParent(relativeTo)
	pop.SetPosition(gtk.PosBottom)

	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.SetMarginTop(8)
	box.SetMarginBottom(8)
	box.SetMarginStart(8)
	box.SetMarginEnd(8)

	lbl := gtk.NewLabel(fmt.Sprintf("Rebase onto %s?", destHash[:7]))
	lbl.AddCSSClass("bold")
	box.Append(lbl)

	msg := gtk.NewLabel(fmt.Sprintf("Move changes from %s to %s", srcHash[:7], destHash[:7]))
	msg.AddCSSClass("dim")
	box.Append(msg)

	btnRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	btnRow.SetHAlign(gtk.AlignEnd)

	cancelBtn := gtk.NewButtonWithLabel("Cancel")
	cancelBtn.ConnectClicked(func() { pop.Popdown() })
	btnRow.Append(cancelBtn)

	confirmBtn := gtk.NewButtonWithLabel("Confirm Rebase")
	confirmBtn.AddCSSClass("suggested-action")
	confirmBtn.ConnectClicked(func() {
		pop.Popdown()
		a.runGitOpSafe("Rebasing...", "Rebase complete", func(repo string) error {
			return RebaseCommit(repo, destHash)
		})
	})
	btnRow.Append(confirmBtn)
	box.Append(btnRow)

	pop.SetChild(box)
	pop.Popup()
}

func (a *App) showCommitContextMenu(relativeTo gtk.Widgetter, c Commit) {
	pop := gtk.NewPopover()
	pop.SetParent(relativeTo)
	pop.SetPosition(gtk.PosBottom)

	box := gtk.NewBox(gtk.OrientationVertical, 0)

	actions := []struct {
		label       string
		destructive bool
		fn          func()
	}{
		{"Checkout " + c.ShortHash, false, func() {
			a.runGitOpSafe(
				"Checking out...",
				"Checked out "+c.ShortHash,
				func(repo string) error {
					return CheckoutCommit(repo, c.Hash)
				},
			)
		}},
		{"Cherry-pick", false, func() {
			a.runGitOpSafe(
				"Cherry-picking...",
				"Cherry-picked "+c.ShortHash,
				func(repo string) error {
					return CherryPickCommit(repo, c.Hash)
				},
			)
		}},
		{"Reset Soft", false, func() {
			a.runGitOpSafe(
				"Resetting...",
				"Reset soft to "+c.ShortHash,
				func(repo string) error {
					return ResetCommit(repo, c.Hash, false)
				},
			)
		}},
		{"Reset Hard", true, func() {
			a.confirmDialog(
				"Reset Hard",
				"This will discard ALL local changes permanently.",
				true,
				func() {
					a.runGitOp(
						"Resetting...",
						"Reset hard to "+c.ShortHash,
						func(repo string) error {
							return ResetCommit(repo, c.Hash, true)
						},
					)
				},
			)
		}},
	}

	for _, act := range actions {
		act := act
		btn := gtk.NewButtonWithLabel(act.label)
		btn.AddCSSClass("flat")
		if act.destructive {
			btn.AddCSSClass("destructive-action")
		}
		btn.ConnectClicked(func() {
			pop.Popdown()
			act.fn()
		})
		box.Append(btn)
	}

	pop.SetChild(box)
	pop.Popup()
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
			coBtn.AddCSSClass("flat") 
			            
			coBtn.ConnectClicked(func() {
				if a.hasUncommittedChanges() {
					a.confirmDialog(
						"Uncommitted Changes",
						"Stash changes before switching to "+b.Name+"?",
						false,
						func() {
							go func(repo, branch string) {
								_ = StashSave(repo, "Auto-stash before checkout")
								err := Checkout(repo, branch)
								glib.IdleAdd(func() {
									if err != nil {
										a.setInfoErr(err.Error())
										a.gitErrorDialog("Checkout Failed", err.Error())
									} else {
										a.setInfoOk("On " + branch)
									}
									a.doReload(true)
								})
							}(a.state.Path, b.Name)
						},
					)
					return
				}
				a.setInfo("Checking out " + b.Name + "…")
				go func(repo, branch string) {
					err := Checkout(repo, branch)
					glib.IdleAdd(func() {
						if err != nil {
							a.setInfoErr(err.Error())
							a.gitErrorDialog("Checkout Failed", err.Error())
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
						a.runGitOpSafe("Merging "+b.Name+"…", "Merged "+b.Name, func(repo string) error {
							return MergeBranch(repo, b.Name)
						})
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
				a.runGitOpSafe("Tracking "+b.Name+"…", "Tracking "+localName, func(repo string) error {
					err := CheckoutNewBranch(repo, localName)
					if err == nil {
						_ = gitCmd2(repo, "branch", "--set-upstream-to", b.Name, localName)
					}
					return err
				})
			})
			acts.Append(trackBtn)
		}

		outer.Append(acts)
		row.SetChild(outer)
		a.branchListBox.Append(row)
	}
}

func (a *App) openStashPanel() {
	if a.state == nil {
		return
	}

	content := gtk.NewBox(gtk.OrientationVertical, 0)

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

func (a *App) openRepoConfigPanel() {
	if a.state == nil || !a.cfg.Features.RepoConfigDialog {
		return
	}

	content := gtk.NewBox(gtk.OrientationVertical, 0)

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
		a.hideOverlay()
		a.runGitOpSafe("Creating branch "+name+"…", "Created "+name, func(repo string) error {
			return CheckoutNewBranch(repo, name)
		})
	})
	btnRow.Append(createBtn)
	content.Append(btnRow)

	card := a.buildOverlayCard("New Branch", 420, content)
	a.showOverlay(card)
}

func (a *App) updateHeaderInfo() {
	if a.state == nil {
		a.repoTitle.SetText("")
		a.repoPath.SetText("")
		a.setInfo("")
		return
	}
	a.repoTitle.SetText(a.state.Name)
	a.repoPath.SetText(a.state.Path)

	if a.headJumpBtn != nil {
		detached := strings.HasPrefix(a.state.Branch, "(detached")
		if detached {
			shortHash := a.state.Branch
			if idx := strings.LastIndex(shortHash, " "); idx >= 0 {
				shortHash = strings.TrimSuffix(shortHash[idx+1:], ")")
			}
			a.headJumpBtn.SetLabel("⚠ " + shortHash)
			a.headJumpBtn.SetTooltipText("Detached HEAD at " + shortHash + " — click to return to branch")
			a.headJumpBtn.SetVisible(true)
		} else {
			a.headJumpBtn.SetVisible(false)
		}
	}

	if time.Now().Before(a.infoStickyUntil) {
		return
	}

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

	a.branchLabel.SetText(a.state.Branch)

	if a.state.Ahead == -1 && a.state.Behind == -1 {
		a.aheadLabel.SetText("! No Upstream")
		a.aheadLabel.SetVisible(true)
		a.setUpstreamFixBtn.SetVisible(true)
	} else if a.state.Ahead > 0 || a.state.Behind > 0 {
		a.aheadLabel.SetText(fmt.Sprintf("↑%d ↓%d", a.state.Ahead, a.state.Behind))
		a.aheadLabel.SetVisible(true)
		a.setUpstreamFixBtn.SetVisible(false)
	} else {
		a.aheadLabel.SetText("")
		a.aheadLabel.SetVisible(false)
		a.setUpstreamFixBtn.SetVisible(false)
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
	if len(parts) > 0 {
		a.statsLabel.SetText("· " + strings.Join(parts, " · "))
	} else {
		a.statsLabel.SetText("")
	}
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
	if a.showSplit {
		a.diffView.SetVisible(false)
		a.splitView.SetVisible(true)
		a.renderSplitDiff(diff)
	} else {
		a.diffView.SetVisible(true)
		a.splitView.SetVisible(false)
		a.renderUnifiedDiff(diff)
	}
}

func (a *App) toggleAllDiffs(expand bool) {
	child := a.diffView.FirstChild()
	for child != nil {
		if exp, ok := child.(*gtk.Expander); ok {
			exp.SetExpanded(expand)
		}
		if w, ok := child.(interface{ NextSibling() gtk.Widgetter }); ok {
			child = w.NextSibling()
		} else {
			break
		}
	}
}

func (a *App) renderUnifiedDiff(diff string) {
	for {
		child := a.diffView.FirstChild()
		if child == nil {
			break
		}
		a.diffView.Remove(child)
	}

	if strings.TrimSpace(diff) == "" {
		buf := gtk.NewTextBuffer(nil)
		a.setupDiffTags(buf)
		buf.SetText("  No changes to display")
		start := buf.StartIter()
		end := buf.EndIter()
		buf.ApplyTagByName("placeholder", start, end)

		tv := gtk.NewTextViewWithBuffer(buf)
		tv.SetEditable(false)
		tv.SetMonospace(true)
		tv.SetLeftMargin(20)
		tv.SetTopMargin(10)
		a.diffView.Append(tv)
		return
	}

	files := a.splitDiffByFile(diff)

	if len(files) == 1 && files[0].Path == "" {
		buf := gtk.NewTextBuffer(nil)
		a.setupDiffTags(buf)
		a.appendDiffToBuffer(buf, files[0].Content)
		tv := gtk.NewTextViewWithBuffer(buf)
		tv.SetEditable(false)
		tv.SetMonospace(true)
		tv.SetLeftMargin(20)
		tv.SetTopMargin(10)
		a.diffView.Append(tv)
		return
	}

	for _, f := range files {
		exp := gtk.NewExpander(f.Path)
		exp.SetExpanded(true)
		exp.AddCSSClass("diff-file-expander")

		buf := gtk.NewTextBuffer(nil)
		a.setupDiffTags(buf)
		a.appendDiffToBuffer(buf, f.Content)

		tv := gtk.NewTextViewWithBuffer(buf)
		tv.SetEditable(false)
		tv.SetCursorVisible(false)
		tv.SetMonospace(true)
		tv.SetLeftMargin(20)
		tv.SetRightMargin(20)
		tv.AddCSSClass("diff-view")

		exp.SetChild(tv)
		a.diffView.Append(exp)
	}

	vadj := a.diffScroll.VAdjustment()
	if vadj != nil {
		vadj.SetValue(0)
	}
}

type fileDiff struct {
	Path    string
	Content string
}

func (a *App) splitDiffByFile(diff string) []fileDiff {
	lines := strings.Split(diff, "\n")
	var files []fileDiff
	var currentFile *fileDiff

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			if currentFile != nil {
				files = append(files, *currentFile)
			}
			parts := strings.Split(line, " ")
			path := ""
			if len(parts) >= 4 {
				path = strings.TrimPrefix(parts[3], "b/")
			}
			currentFile = &fileDiff{Path: path, Content: line + "\n"}
		} else if currentFile != nil {
			currentFile.Content += line + "\n"
		} else {
			currentFile = &fileDiff{Path: "", Content: line + "\n"}
		}
	}
	if currentFile != nil {
		files = append(files, *currentFile)
	}
	return files
}

func (a *App) appendDiffToBuffer(buf *gtk.TextBuffer, diff string) {
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	for _, line := range lines {
		tag := a.getDiffTag(line)
		iter := buf.EndIter()
		offset := iter.Offset()
		buf.Insert(iter, line+"\n")

		if tag != "normal" {
			start := buf.IterAtOffset(offset)
			end := buf.EndIter()
			buf.ApplyTagByName(tag, start, end)
			if tag == "added" || tag == "removed" {
				charStart := buf.IterAtOffset(offset)
				charEnd := buf.IterAtOffset(offset + 1)
				buf.ApplyTagByName(tag+"-char", charStart, charEnd)
			}
		}
	}
}

func (a *App) renderSplitDiff(diff string) {
	a.diffBufLeft.SetText("")
	a.diffBufRight.SetText("")
	a.setupDiffTags(a.diffBufLeft)
	a.setupDiffTags(a.diffBufRight)

	if strings.TrimSpace(diff) == "" {
		return
	}

	lines := strings.Split(diff, "\n")
	for _, line := range lines {
		tag := a.getDiffTag(line)
		iterL := a.diffBufLeft.EndIter()
		iterR := a.diffBufRight.EndIter()
		offL := iterL.Offset()
		offR := iterR.Offset()

		if tag == "added" {
			a.diffBufLeft.Insert(iterL, "\n")
			a.diffBufRight.Insert(iterR, line[1:]+"\n")
			startR := a.diffBufRight.IterAtOffset(offR)
			endR := a.diffBufRight.EndIter()
			a.diffBufRight.ApplyTagByName("added", startR, endR)
		} else if tag == "removed" {
			a.diffBufLeft.Insert(iterL, line[1:]+"\n")
			startL := a.diffBufLeft.IterAtOffset(offL)
			endL := a.diffBufLeft.EndIter()
			a.diffBufLeft.ApplyTagByName("removed", startL, endL)
			a.diffBufRight.Insert(iterR, "\n")
		} else if tag == "hunk" || tag == "header" {
			a.diffBufLeft.Insert(iterL, line+"\n")
			startL := a.diffBufLeft.IterAtOffset(offL)
			endL := a.diffBufLeft.EndIter()
			a.diffBufLeft.ApplyTagByName(tag, startL, endL)

			a.diffBufRight.Insert(iterR, line+"\n")
			startR := a.diffBufRight.IterAtOffset(offR)
			endR := a.diffBufRight.EndIter()
			a.diffBufRight.ApplyTagByName(tag, startR, endR)
		} else {
			text := line
			if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
				text = line[1:]
			}
			a.diffBufLeft.Insert(iterL, text+"\n")
			a.diffBufRight.Insert(iterR, text+"\n")
		}
	}
}

func (a *App) getDiffTag(line string) string {
	switch {
	case strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "index "),
		strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
		return "header"
	case strings.HasPrefix(line, "@@"):
		return "hunk"
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
		return "added"
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
		return "removed"
	case strings.HasPrefix(line, "rename "), strings.HasPrefix(line, "similarity "):
		return "modified"
	default:
		return "normal"
	}
}

func (a *App) setupDiffTags(buf *gtk.TextBuffer) {
	tt := buf.TagTable()
	type tagDef struct {
		name string
		fg   string
		bg   string
	}
	
	tags := []tagDef{
		{"added",        "#a8d8a8", "#1a3320"},
		{"removed",      "#e89090", "#331a1a"},
		{"added-char",   "#4ade80", "#1a3320"},
		{"removed-char", "#f87171", "#331a1a"},
		{"header",       "#c9955c", ""},
		{"hunk",         "#7a9fbe", ""},
		{"normal",       "#c8c4bc", ""},
		{"modified",     "#b89a5a", ""},
		{"placeholder",  "#4a4540", ""},
	}
	for _, td := range tags {
		if tt.Lookup(td.name) != nil {
			continue
		}
		tag := gtk.NewTextTag(td.name)
		if td.fg != "" {
			tag.SetObjectProperty("foreground", td.fg)
		}
		if td.bg != "" {
			tag.SetObjectProperty("background", td.bg)
			tag.SetObjectProperty("paragraph-background", td.bg)
		}
		tt.Add(tag)
	}
}

func (a *App) nextDiffRequestID() uint64 {
	return atomic.AddUint64(&a.diffReqID, 1)
}

func (a *App) loadFileDiff(path string, staged bool) {
	if a.state == nil {
		return
	}
	a.commitHeader.SetVisible(false)
	if a.copyHashBtn != nil {
		a.copyHashBtn.SetVisible(false)
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
	if a.copyHashBtn != nil {
		a.copyHashBtn.SetVisible(true)
	}

	var commit *Commit
	for i := range a.state.Commits {
		if a.state.Commits[i].Hash == hash {
			commit = &a.state.Commits[i]
			break
		}
	}

	if commit != nil {
		a.commitAuthor.SetText(fmt.Sprintf("%s <%s>", commit.Author, commit.AuthorEmail))
		a.commitHashFull.SetText(commit.Hash)
		a.commitDateFull.SetText(commit.Date.Format("Mon Jan 2 15:04:05 2006 -0700"))
		a.commitSubjectBold.SetText(commit.Subject)
		a.commitHeader.SetVisible(true)
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

func (a *App) openSetUpstreamDialog() {
	if a.state == nil {
		return
	}

	content := gtk.NewBox(gtk.OrientationVertical, 12)
	content.SetMarginTop(16)
	content.SetMarginBottom(16)
	content.SetMarginStart(16)
	content.SetMarginEnd(16)

	infoLbl := gtk.NewLabel("Configure upstream tracking for branch: " + a.state.Branch)
	infoLbl.AddCSSClass("dim")
	infoLbl.SetXAlign(0)
	content.Append(infoLbl)

	grid := gtk.NewGrid()
	grid.SetColumnSpacing(10)
	grid.SetRowSpacing(10)

	grid.Attach(gtk.NewLabel("Remote:"), 0, 0, 1, 1)
	
	remotes := GetRemotes(a.state.Path)
	var remoteNames []string
	for _, r := range remotes {
		remoteNames = append(remoteNames, r.Name)
	}
	if len(remoteNames) == 0 {
		remoteNames = []string{"origin"}
	}
	
	remoteDrop := gtk.NewDropDown(gtk.NewStringList(remoteNames), nil)
	grid.Attach(remoteDrop, 1, 0, 1, 1)

	grid.Attach(gtk.NewLabel("Branch:"), 0, 1, 1, 1)
	branchEntry := gtk.NewEntry()
	branchEntry.SetText(a.state.Branch)
	branchEntry.SetHExpand(true)
	grid.Attach(branchEntry, 1, 1, 1, 1)

	content.Append(grid)

	btnRow := gtk.NewBox(gtk.OrientationHorizontal, 8)
	btnRow.SetHAlign(gtk.AlignEnd)

	cancelBtn := gtk.NewButtonWithLabel("Cancel")
	cancelBtn.ConnectClicked(func() { a.hideOverlay() })
	btnRow.Append(cancelBtn)

	okBtn := gtk.NewButtonWithLabel("Set Upstream")
	okBtn.AddCSSClass("suggested-action")
	okBtn.ConnectClicked(func() {
		sel := remoteDrop.Selected()
		if sel == gtk.InvalidListPosition {
			return
		}
		modelObj := remoteDrop.Model()
		stringList := modelObj.Cast().(*gtk.StringList)
		remote := stringList.String(sel)
		branch := strings.TrimSpace(branchEntry.Text())
		
		a.hideOverlay()
		a.runGitOp("Setting upstream...", "Upstream configured", func(repo string) error {
			return PushSetUpstream(repo, remote, branch)
		})
	})
	btnRow.Append(okBtn)
	content.Append(btnRow)

	card := a.buildOverlayCard("Set Upstream", 440, content)
	a.showOverlay(card)
}
func (a *App) populateStashes()      { a.populateStashList() }

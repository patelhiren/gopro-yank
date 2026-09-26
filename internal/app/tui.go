package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type tuiScreen int

const (
	screenHome tuiScreen = iota
	screenLibrary
	screenConfirm
	screenPath
	screenProgress
	screenResult
	screenDeleteConfirm
	screenError
	screenSelection
)

// The selection screen has two date fields followed by one checkbox per media type.
const (
	selectFrom = iota
	selectTo
	selectFieldCount
)

type tuiAction struct {
	id          string
	label, help string
}

type loginFinishedMsg struct{ err error }
type inspectionFinishedMsg struct {
	inspection LibraryInspection
	err        error
}
type verificationFinishedMsg struct {
	result VerifyResult
	err    error
}
type deletionFinishedMsg struct {
	result DeleteResult
	err    error
}
type openFinishedMsg struct{ err error }

type archiveMessage struct {
	event  *ArchiveEvent
	result *ArchiveResult
	err    error
}

type tuiModel struct {
	ctx         context.Context
	version     string
	demo        bool
	screen      tuiScreen
	width       int
	height      int
	dark        bool
	connected   bool
	archiveRoot string
	envPath     string
	archive     *Archive
	inspection  *LibraryInspection
	cursor      int
	busy        bool
	busyText    string
	status      string
	err         error
	spinner     spinner.Model
	progress    progress.Model
	pathInput   textinput.Model
	deleteInput textinput.Model
	// selection nil keeps the folder's saved selection; set once chosen here.
	selection    *Selection
	selectInputs [selectFieldCount]textinput.Model
	selectFocus  int
	selectErr    error
	typeOptions  []string
	typeCounts   map[string]int
	typeChecked  map[string]bool
	pathNote     string
	cancel       context.CancelFunc
	events       <-chan archiveMessage
	archiveRun   ArchiveResult
	verifyRun    VerifyResult
	recent       []DownloadResult
	deletePlan   DeletePlan
	errorNote    string
}

func newTUIModel(ctx context.Context, version string, demo bool) tuiModel {
	spin := spinner.New(spinner.WithSpinner(spinner.Dot))
	bar := progress.New(
		progress.WithDefaultBlend(),
		progress.WithFillCharacters('━', '─'),
		progress.WithoutPercentage(),
	)
	input := textinput.New()
	input.Prompt = "Archive folder  "
	input.CharLimit = 1024
	input.SetValue(defaultArchiveRoot())
	input.CursorEnd()
	deleteInput := textinput.New()
	deleteInput.Prompt = "Type DELETE  "
	deleteInput.CharLimit = len("DELETE")
	var selectInputs [selectFieldCount]textinput.Model
	for index, field := range []struct{ prompt, placeholder string }{
		{"From   ", "YYYY-MM-DD or YYYY-MM-DD HH:MM"},
		{"To     ", "same format, included"},
	} {
		selectInputs[index] = textinput.New()
		selectInputs[index].Prompt = field.prompt
		selectInputs[index].Placeholder = field.placeholder
		selectInputs[index].CharLimit = 200
		selectInputs[index].SetWidth(48)
	}
	archive, _ := NewArchive(defaultArchiveRoot())
	model := tuiModel{
		selectInputs: selectInputs,
		ctx:          ctx,
		version:      version,
		demo:         demo,
		screen:       screenHome,
		archiveRoot:  defaultArchiveRoot(),
		envPath:      defaultEnvFile(),
		archive:      archive,
		spinner:      spin,
		progress:     bar,
		pathInput:    input,
		deleteInput:  deleteInput,
	}
	if _, _, err := loadCredentials(model.envPath); err == nil {
		model.connected = true
	}
	if demo {
		model.connected = true
		model.archive = &Archive{Root: model.archiveRoot}
		inspection := demoLibraryInspection(model.archiveRoot)
		model.inspection = &inspection
		model.screen = screenLibrary
	}
	return model
}

func demoLibraryInspection(root string) LibraryInspection {
	return LibraryInspection{
		Total:          847,
		TotalBytes:     386 * 1000 * 1000 * 1000,
		Archived:       312,
		ArchivedBytes:  142 * 1000 * 1000 * 1000,
		Remaining:      531,
		RemainingBytes: 244 * 1000 * 1000 * 1000,
		Manual:         4,
		Earliest:       "2018-06-14",
		Latest:         "2026-07-28",
		Types:          map[string]int{"Photo": 493, "Video": 350, "MultiClipEdit": 4},
		ArchiveRoot:    root,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.spinner.Tick)
}

func (m tuiModel) homeActions() []tuiAction {
	actions := []tuiAction{}
	if !m.connected {
		actions = append(actions, tuiAction{"connect", "Connect GoPro", "Sign in safely on GoPro's website"})
	} else {
		actions = append(actions, tuiAction{"library", "View GoPro library", "Read-only — nothing downloads"})
	}
	if m.archive != nil && m.archive.Exists {
		actions = append(actions, tuiAction{"verify", "Check archive", "Read and verify every saved file"})
		if _, err := os.Stat(m.archive.ReportPath); err == nil {
			actions = append(actions, tuiAction{"report", "Open report", "View the archive report in your browser"})
		}
		actions = append(actions, tuiAction{"delete", "Delete local archive", "Remove its saved files from this computer"})
	}
	actions = append(actions, tuiAction{"quit", "Quit", "Leave everything as it is"})
	return actions
}

func (m *tuiModel) reloadArchive() {
	if m.demo {
		m.archive = &Archive{Root: m.archiveRoot}
		return
	}
	archive, err := NewArchive(m.archiveRoot)
	if err == nil {
		m.archive = archive
	}
}

func (m *tuiModel) startTask(label string) context.Context {
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	m.busy = true
	m.busyText = label
	m.status = ""
	m.err = nil
	m.errorNote = ""
	return ctx
}

func loginCmd(ctx context.Context, envPath string) tea.Cmd {
	return func() tea.Msg { return loginFinishedMsg{err: connectAccountInBrowser(ctx, envPath)} }
}

func inspectCmd(ctx context.Context, root, envPath string) tea.Cmd {
	return func() tea.Msg {
		inspection, err := InspectLibrary(ctx, root, envPath, 100, nil)
		return inspectionFinishedMsg{inspection: inspection, err: err}
	}
}

func verifyCmd(ctx context.Context, root string) tea.Cmd {
	return func() tea.Msg {
		result, err := VerifyArchive(ctx, root, "")
		return verificationFinishedMsg{result: result, err: err}
	}
}

func deleteCmd(ctx context.Context, root string) tea.Cmd {
	return func() tea.Msg {
		result, err := DeleteLocalArchive(ctx, root)
		return deletionFinishedMsg{result: result, err: err}
	}
}

func openCmd(path string) tea.Cmd {
	return func() tea.Msg { return openFinishedMsg{err: openURL("file://" + filepath.ToSlash(path))} }
}

func waitArchiveMessage(events <-chan archiveMessage) tea.Cmd {
	return func() tea.Msg { return <-events }
}

func startArchive(ctx context.Context, options ArchiveOptions, demo bool) <-chan archiveMessage {
	events := make(chan archiveMessage, 64)
	go func() {
		defer close(events)
		if demo {
			start := time.Now()
			var transferred int64
			for index := 1; index <= 12; index++ {
				select {
				case <-ctx.Done():
					events <- archiveMessage{err: ctx.Err()}
					return
				case <-time.After(120 * time.Millisecond):
				}
				download := DownloadResult{MediaID: fmt.Sprintf("demo-%03d", index), Status: "ok", Bytes: int64(140+index*9) * 1000 * 1000}
				transferred += download.Bytes
				event := ArchiveEvent{Stage: "Archiving originals", Current: index, Total: 12, Transferred: transferred, Result: &download}
				events <- archiveMessage{event: &event}
			}
			result := ArchiveResult{Transferred: transferred, Elapsed: time.Since(start), Summary: Summary{Archived: 324, Files: 324, Bytes: 144 * 1000 * 1000 * 1000, Manual: 4}, Verification: VerificationResult{CheckedItems: 324, CheckedFiles: 324, CheckedBytes: 144 * 1000 * 1000 * 1000}}
			events <- archiveMessage{result: &result}
			return
		}
		result, err := ArchiveLibrary(ctx, options, func(event ArchiveEvent) {
			copy := event
			select {
			case events <- archiveMessage{event: &copy}:
			case <-ctx.Done():
			}
		})
		events <- archiveMessage{result: &result, err: err}
	}()
	return events
}

func (m *tuiModel) beginArchive() tea.Cmd {
	ctx := m.startTask("Preparing your archive")
	m.screen = screenProgress
	m.recent = nil
	m.archiveRun = ArchiveResult{}
	m.progress.SetPercent(0)
	m.events = startArchive(ctx, ArchiveOptions{
		Root:        m.archiveRoot,
		EnvPath:     m.envPath,
		LegacyState: defaultLegacyState(),
		Parallel:    8,
		PerPage:     100,
		Selection:   m.selection,
	}, m.demo)
	return waitArchiveMessage(m.events)
}

func (m *tuiModel) stopTask() {
	if m.cancel != nil {
		m.cancel()
	}
	m.status = "Stopping safely — finished files will be kept"
}

func normalizeArchivePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("archive folder cannot be empty")
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	return filepath.Abs(value)
}

func (m tuiModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch msg := message.(type) {
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.pathInput.SetStyles(textinput.DefaultStyles(m.dark))
		m.deleteInput.SetStyles(textinput.DefaultStyles(m.dark))
		for index := range m.selectInputs {
			m.selectInputs[index].SetStyles(textinput.DefaultStyles(m.dark))
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.progress.SetWidth(max(20, min(72, msg.Width-12)))
		m.pathInput.SetWidth(max(20, min(72, msg.Width-20)))
		for index := range m.selectInputs {
			m.selectInputs[index].SetWidth(max(20, min(60, msg.Width-20)))
		}
	case spinner.TickMsg:
		var command tea.Cmd
		m.spinner, command = m.spinner.Update(msg)
		commands = append(commands, command)
	case progress.FrameMsg:
		var command tea.Cmd
		m.progress, command = m.progress.Update(msg)
		commands = append(commands, command)
	case loginFinishedMsg:
		m.busy, m.cancel = false, nil
		if errors.Is(msg.err, context.Canceled) {
			m.screen = screenHome
			m.status = "Stopped"
		} else if msg.err != nil {
			m.err, m.screen = msg.err, screenError
		} else {
			m.connected = true
			m.status = "Connected to GoPro"
			m.cursor = 0
		}
	case inspectionFinishedMsg:
		m.busy, m.cancel = false, nil
		if errors.Is(msg.err, context.Canceled) {
			m.screen = screenHome
			m.status = "Stopped"
		} else if msg.err != nil {
			if errors.Is(msg.err, ErrAuth) {
				m.connected = false
			}
			m.err, m.screen = msg.err, screenError
		} else {
			m.inspection = &msg.inspection
			m.screen = screenLibrary
		}
	case verificationFinishedMsg:
		m.busy, m.cancel = false, nil
		m.verifyRun = msg.result
		if errors.Is(msg.err, context.Canceled) {
			m.screen = screenHome
			m.status = "Stopped"
		} else if msg.err != nil && !errors.Is(msg.err, ErrArchiveIncomplete) {
			m.err, m.screen = msg.err, screenError
		} else {
			m.screen = screenResult
			m.status = "Archive check complete"
			m.reloadArchive()
		}
	case deletionFinishedMsg:
		m.busy, m.cancel = false, nil
		if msg.err != nil {
			m.err, m.screen = msg.err, screenError
			m.errorNote = "Deletion did not finish. Some local files may already be gone. GoPro cloud media was not touched."
		} else {
			m.reloadArchive()
			m.inspection = nil
			m.screen, m.cursor = screenHome, 0
			m.status = fmt.Sprintf("Deleted %d local file(s) · %s. GoPro cloud media was not touched.", msg.result.RemovedFiles, humanBytes(msg.result.RemovedBytes))
		}
	case openFinishedMsg:
		m.busy = false
		if msg.err != nil {
			m.err, m.screen = msg.err, screenError
		} else {
			m.status = "Opened the archive report"
		}
	case archiveMessage:
		if msg.event != nil {
			m.busyText = msg.event.Stage
			if msg.event.Result != nil {
				m.recent = append(m.recent, *msg.event.Result)
				if len(m.recent) > 5 {
					m.recent = m.recent[len(m.recent)-5:]
				}
			}
			if msg.event.Total > 0 {
				command := m.progress.SetPercent(float64(msg.event.Current) / float64(msg.event.Total))
				commands = append(commands, command)
			}
			commands = append(commands, waitArchiveMessage(m.events))
		} else {
			m.busy, m.cancel = false, nil
			if msg.result != nil {
				m.archiveRun = *msg.result
			}
			if errors.Is(msg.err, context.Canceled) {
				m.screen, m.cursor = screenHome, 0
				m.status = "Stopped safely. Run archive again to continue."
				m.reloadArchive()
			} else if msg.err != nil && !errors.Is(msg.err, ErrArchiveIncomplete) {
				m.err, m.screen = msg.err, screenError
			} else {
				m.screen = screenResult
				m.status = "Archive run complete"
				m.reloadArchive()
			}
		}
	}

	key, isKey := message.(tea.KeyPressMsg)
	if !isKey {
		return m, tea.Batch(commands...)
	}
	stroke := key.String()
	if m.screen == screenPath {
		switch stroke {
		case "esc":
			m.pathInput.Blur()
			m.pathNote = ""
			m.screen = screenLibrary
			return m, nil
		case "enter":
			root, err := normalizeArchivePath(m.pathInput.Value())
			if err != nil {
				m.pathInput.Err = err
				return m, nil
			}
			m.archiveRoot = root
			m.pathInput.Blur()
			m.pathNote = ""
			if m.demo {
				inspection := demoLibraryInspection(root)
				m.inspection = &inspection
			} else if m.inspection != nil {
				inspection, planErr := SelectLibrary(root, *m.inspection, m.selection)
				if planErr != nil {
					m.err, m.screen = planErr, screenError
					return m, nil
				}
				m.inspection = &inspection
			}
			m.reloadArchive()
			m.screen = screenLibrary
			return m, nil
		}
		var command tea.Cmd
		m.pathInput, command = m.pathInput.Update(message)
		return m, command
	}
	if m.screen == screenSelection {
		return m.updateSelection(message, stroke)
	}
	if m.screen == screenDeleteConfirm && !m.busy {
		switch stroke {
		case "esc":
			m.deleteInput.Blur()
			m.deleteInput.Err = nil
			m.screen = screenHome
			return m, nil
		case "enter":
			if m.deleteInput.Value() != "DELETE" {
				m.deleteInput.Err = errors.New("type DELETE exactly to continue")
				return m, nil
			}
			m.deleteInput.Blur()
			ctx := m.startTask("Deleting the local archive — GoPro cloud untouched")
			return m, deleteCmd(ctx, m.archiveRoot)
		}
		var command tea.Cmd
		m.deleteInput, command = m.deleteInput.Update(message)
		return m, command
	}
	if stroke == "ctrl+c" || stroke == "q" {
		if m.busy {
			m.stopTask()
			return m, nil
		}
		return m, tea.Quit
	}
	if m.busy {
		if stroke == "esc" {
			m.stopTask()
		}
		return m, tea.Batch(commands...)
	}

	switch m.screen {
	case screenHome:
		actions := m.homeActions()
		switch stroke {
		case "up", "k":
			m.cursor = (m.cursor - 1 + len(actions)) % len(actions)
		case "down", "j":
			m.cursor = (m.cursor + 1) % len(actions)
		case "enter":
			action := actions[m.cursor].id
			switch action {
			case "connect":
				ctx := m.startTask("Waiting for you to sign in on GoPro")
				commands = append(commands, loginCmd(ctx, m.envPath))
			case "library":
				ctx := m.startTask("Reading your GoPro library — nothing will download")
				commands = append(commands, inspectCmd(ctx, m.archiveRoot, m.envPath))
			case "verify":
				ctx := m.startTask("Checking every archived file")
				commands = append(commands, verifyCmd(ctx, m.archiveRoot))
			case "report":
				m.busy, m.busyText = true, "Opening your archive report"
				commands = append(commands, openCmd(m.archive.ReportPath))
			case "delete":
				plan, err := PlanLocalArchive(m.archiveRoot)
				if err != nil {
					m.err, m.screen = err, screenError
					break
				}
				m.deletePlan = plan
				m.deleteInput.SetValue("")
				m.deleteInput.Err = nil
				m.deleteInput.Focus()
				m.screen = screenDeleteConfirm
			case "quit":
				commands = append(commands, tea.Quit)
			}
		}
	case screenLibrary:
		switch stroke {
		case "enter", "a":
			if m.inspection != nil {
				m.screen = screenConfirm
			}
		case "e":
			m.pathNote = ""
			m.pathInput.SetValue(m.archiveRoot)
			m.pathInput.CursorEnd()
			m.pathInput.Focus()
			m.screen = screenPath
		case "s":
			if m.inspection != nil {
				m.openSelection()
			}
		case "esc", "b":
			m.screen, m.cursor = screenHome, 0
		}
	case screenConfirm:
		switch stroke {
		case "enter", "a":
			commands = append(commands, m.beginArchive())
		case "e":
			m.pathNote = ""
			m.pathInput.SetValue(m.archiveRoot)
			m.pathInput.CursorEnd()
			m.pathInput.Focus()
			m.screen = screenPath
		case "esc", "b":
			m.screen = screenLibrary
		}
	case screenProgress:
		if stroke == "esc" {
			m.stopTask()
		}
	case screenResult:
		switch stroke {
		case "o":
			path := m.archiveRun.ReportPath
			if path == "" {
				path = m.verifyRun.ReportPath
			}
			if path == "" && m.archive != nil {
				path = m.archive.ReportPath
			}
			if path != "" {
				m.busy, m.busyText = true, "Opening your archive report"
				commands = append(commands, openCmd(path))
			}
		case "enter", "esc", "b":
			m.screen, m.cursor = screenHome, 0
		}
	case screenError:
		if stroke == "enter" || stroke == "esc" || stroke == "b" {
			m.err, m.errorNote, m.screen, m.cursor = nil, "", screenHome, 0
		}
	}
	return m, tea.Batch(commands...)
}

// openSelection fills the selection fields from the library's current selection
// and lists every media type in the library as a checkbox.
func (m *tuiModel) openSelection() {
	current := m.inspection.Selection
	m.selectInputs[selectFrom].SetValue(current.From)
	m.selectInputs[selectTo].SetValue(current.To)
	for index := range m.selectInputs {
		m.selectInputs[index].CursorEnd()
	}
	m.typeCounts = map[string]int{}
	for _, item := range m.inspection.library {
		// Edits never download, so they are not offered as a type to choose.
		if item.MediaType != "MultiClipEdit" {
			m.typeCounts[item.MediaType]++
		}
	}
	m.typeChecked = map[string]bool{}
	for _, kind := range current.Types {
		matched := false
		for name := range m.typeCounts {
			if strings.EqualFold(name, kind) {
				m.typeChecked[name], matched = true, true
			}
		}
		if !matched {
			m.typeCounts[kind], m.typeChecked[kind] = 0, true
		}
	}
	m.typeOptions = sortedTypeNames(m.typeCounts)
	m.selectErr = nil
	m.focusSelection(selectFrom)
	m.screen = screenSelection
}

func (m *tuiModel) focusSelection(field int) {
	count := selectFieldCount + len(m.typeOptions)
	m.selectFocus = (field + count) % count
	for index := range m.selectInputs {
		if index == m.selectFocus {
			m.selectInputs[index].Focus()
		} else {
			m.selectInputs[index].Blur()
		}
	}
}

// focusedType returns the media type checkbox under the cursor, if any.
func (m tuiModel) focusedType() (string, bool) {
	if m.selectFocus < selectFieldCount {
		return "", false
	}
	return m.typeOptions[m.selectFocus-selectFieldCount], true
}

func (m tuiModel) checkedTypes() string {
	checked := []string{}
	for _, name := range m.typeOptions {
		if m.typeChecked[name] {
			checked = append(checked, name)
		}
	}
	return strings.Join(checked, ",")
}

// selectionFoldersRoot holds the folders suggested for selections.
func selectionFoldersRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "GoPro")
}

// suggestedFolder names a folder in parent for selection, such as
// gopro-2026-08-24-to-2026-08-30-video.
func suggestedFolder(parent string, selection Selection) string {
	dates := strings.NewReplacer("T", "-", " ", "-", ":", "")
	parts := []string{"gopro"}
	switch {
	case selection.From != "" && selection.To != "":
		parts = append(parts, dates.Replace(selection.From), "to", dates.Replace(selection.To))
	case selection.From != "":
		parts = append(parts, "from", dates.Replace(selection.From))
	case selection.To != "":
		parts = append(parts, "through", dates.Replace(selection.To))
	}
	for _, kind := range selection.Types {
		parts = append(parts, strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, strings.ToLower(kind)))
	}
	return filepath.Join(parent, strings.Join(parts, "-"))
}

// folderMatches reports whether the current folder already holds this selection.
func (m tuiModel) folderMatches(selection Selection) bool {
	return m.archive != nil && m.archive.Exists && m.archive.Data.Selection != nil && m.archive.Data.Selection.Equal(selection)
}

// selectionZone keeps the zone of an existing date selection, such as one made
// with --tz or --camera-clock, and otherwise follows gopro.com on this computer.
func (m tuiModel) selectionZone() string {
	if m.inspection != nil {
		if current := m.inspection.Selection; current.From != "" || current.To != "" {
			return current.Zone
		}
	}
	return localZoneName()
}

func (m tuiModel) updateSelection(message tea.Msg, stroke string) (tea.Model, tea.Cmd) {
	switch stroke {
	case "esc":
		for index := range m.selectInputs {
			m.selectInputs[index].Blur()
		}
		m.selectErr = nil
		m.screen = screenLibrary
		return m, nil
	case "space", " ", "x":
		if kind, ok := m.focusedType(); ok {
			m.typeChecked[kind] = !m.typeChecked[kind]
			return m, nil
		}
	case "tab", "down":
		m.focusSelection(m.selectFocus + 1)
		return m, nil
	case "shift+tab", "up":
		m.focusSelection(m.selectFocus - 1)
		return m, nil
	case "enter":
		selection, err := ParseSelection(m.selectInputs[selectFrom].Value(), m.selectInputs[selectTo].Value(), m.checkedTypes(), m.selectionZone())
		if err != nil {
			m.selectErr = err
			return m, nil
		}
		if m.demo {
			m.selectErr = errors.New("the demo has no media to choose from; connect GoPro to choose dates")
			return m, nil
		}
		inspection, err := SelectLibrary(m.archiveRoot, *m.inspection, &selection)
		if err != nil {
			m.selectErr = err
			return m, nil
		}
		if inspection.Total == 0 {
			m.selectErr = fmt.Errorf("no GoPro media matches %s", selection)
			return m, nil
		}
		m.selection, m.inspection, m.selectErr = &selection, &inspection, nil
		for index := range m.selectInputs {
			m.selectInputs[index].Blur()
		}
		m.screen = screenLibrary
		if !selection.IsEmpty() && !m.folderMatches(selection) {
			m.pathInput.SetValue(suggestedFolder(selectionFoldersRoot(), selection))
			m.pathInput.CursorEnd()
			m.pathInput.Err = nil
			m.pathInput.Focus()
			m.pathNote = "Suggested a new folder for this selection. Edit it, press enter to use it, or esc to keep " + m.archiveRoot + "."
			m.screen = screenPath
		}
		return m, nil
	}
	if m.selectFocus >= selectFieldCount {
		return m, nil
	}
	var command tea.Cmd
	m.selectInputs[m.selectFocus], command = m.selectInputs[m.selectFocus].Update(message)
	return m, command
}

// selectionChange describes how archiving would change the folder's saved
// selection, or returns "" when it stays the same.
func (m tuiModel) selectionChange() string {
	if m.selection == nil || m.archive == nil || !m.archive.Exists {
		return ""
	}
	saved := Selection{}
	if m.archive.Data.Selection != nil {
		saved = *m.archive.Data.Selection
	}
	if saved.Equal(*m.selection) {
		return ""
	}
	return fmt.Sprintf("This folder's selection changes from %s to %s. Files already saved stay.", saved, *m.selection)
}

func (m tuiModel) View() tea.View {
	content := m.render()
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "GoPro Yank"
	return view
}

func runTUI(ctx context.Context, version string, demo bool) error {
	_, err := tea.NewProgram(newTUIModel(ctx, version, demo), tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return exitError{130, ctx.Err()}
	}
	return err
}

func terminalIsInteractive() bool {
	input, inputErr := os.Stdin.Stat()
	output, outputErr := os.Stdout.Stat()
	return inputErr == nil && outputErr == nil && input.Mode()&os.ModeCharDevice != 0 && output.Mode()&os.ModeCharDevice != 0
}

func sortedTypeNames(types map[string]int) []string {
	names := make([]string, 0, len(types))
	for name := range types {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

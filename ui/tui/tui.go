package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hubertat/swkit/agent"
	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/logging"
)

// IoDebugFilter controls which IO points are shown
type IoDebugFilter int

const (
	IoFilterAll IoDebugFilter = iota
	IoFilterInputs
	IoFilterOutputs
	IoFilterAnalog
)

// ioFilterCount is the number of IO debug filters (used for cycling).
const ioFilterCount = 4

func (f IoDebugFilter) String() string {
	switch f {
	case IoFilterInputs:
		return "Inputs"
	case IoFilterOutputs:
		return "Outputs"
	case IoFilterAnalog:
		return "Analog"
	default:
		return "All"
	}
}

// Tab represents a navigation tab
type Tab int

const (
	TabDashboard Tab = iota
	TabDrivers
	TabDevices
	TabConfig
	TabHomeKit
	TabIoDebug
	TabChat
	TabLogs
)

func (t Tab) String() string {
	switch t {
	case TabDashboard:
		return "Dashboard"
	case TabDrivers:
		return "Drivers"
	case TabDevices:
		return "Devices"
	case TabConfig:
		return "Config"
	case TabHomeKit:
		return "HomeKit"
	case TabIoDebug:
		return "IO Debug"
	case TabChat:
		return "Chat"
	case TabLogs:
		return "Logs"
	default:
		return "Unknown"
	}
}

// AllTabs returns all available tabs
func AllTabs() []Tab {
	return []Tab{TabDashboard, TabDrivers, TabDevices, TabConfig, TabHomeKit, TabIoDebug, TabChat, TabLogs}
}

// Model is the main TUI model
type Model struct {
	provider       app.StateProvider
	state          app.AppState
	stateCh        <-chan app.AppState // single long-lived subscription
	activeTab      Tab
	keys           KeyMap
	theme          Theme
	width, height  int
	showHelp       bool
	cursor         int // For list navigation within tabs
	ioDebugFilter  IoDebugFilter
	ioNames        map[string]string    // session-only custom names, key: "driver|type|index"
	ioPrevStates   map[string]bool      // last seen State per IO point for TUI-side change detection
	ioStateChanged map[string]time.Time // when TUI last observed a state change for an IO point
	ioNamesMan     app.IoNamesManager   // non-nil when provider supports it; nil → use ioNames fallback
	ioNaming       bool                 // true when text input is active for naming
	ioNameInput    textinput.Model
	ioNameTarget   string // key of the IO point being named
	ioExportMsg    string // transient status message after export
	ioImporting    bool   // true when filename input is active for import
	ioImportInput  textinput.Model
	timedPrompt    bool // true when the "on for N seconds" duration input is active
	timedInput     textinput.Model
	timedTarget    int // device index being timed-controlled

	// configDiscardConfirm is true while the "discard your edits and load
	// the latest config?" modal is up, offered after Ctrl+S/Ctrl+R hits a
	// save conflict (see ConfigEditor.saveConflict) while the editor is
	// dirty. Only an explicit "y" here calls ConfigEditor.discardAndReload -
	// see the ConfigSaveMsg and confirmation-key handling below.
	configDiscardConfirm bool
	ctx                  context.Context
	cancel               context.CancelFunc
	chat                 ChatView
	logs                 *LogsView
	agent                *agent.Agent
	configEditor         ConfigEditor

	// idleTimeout, when non-zero, is the period of no user input after
	// which the session's idle watchdog quits it. Only key (and mouse)
	// messages count — state and log updates arrive on their own regardless
	// of whether anyone is watching, which is exactly why a transport-level
	// deadline cannot measure this. Note the programs do not enable mouse
	// reporting, so keystrokes are what keep a session alive in practice.
	// Zero disables the watchdog; the local non-SSH TUI always gets zero.
	idleTimeout time.Duration
	// lastInput is updated on every tea.KeyMsg/tea.MouseMsg and read by the
	// idle watchdog tick to decide whether the session has gone idle.
	lastInput time.Time
}

// StateUpdateMsg is sent when state is updated
type StateUpdateMsg struct {
	State app.AppState
}

// ControlResultMsg is sent when a device control operation completes
type ControlResultMsg struct {
	Result app.ControlResult
}

// NewModel creates a new TUI model using the default renderer
func NewModel(provider app.StateProvider) Model {
	return NewModelWithOptions(provider, nil, nil, nil, nil)
}

// NewModelWithAgent creates a new TUI model with an optional agent
func NewModelWithAgent(provider app.StateProvider, ag *agent.Agent) Model {
	return NewModelWithOptions(provider, nil, ag, nil, nil)
}

// NewModelWithRenderer creates a new TUI model with a custom renderer.
// This is needed for SSH sessions where each connection has its own renderer.
func NewModelWithRenderer(provider app.StateProvider, renderer *lipgloss.Renderer) Model {
	return NewModelWithOptions(provider, nil, nil, renderer, nil)
}

// NewModelWithRendererAndAgent creates a new TUI model with a custom renderer and optional agent.
func NewModelWithRendererAndAgent(provider app.StateProvider, renderer *lipgloss.Renderer, ag *agent.Agent, bc *logging.Broadcaster) Model {
	return NewModelWithOptions(provider, nil, ag, renderer, bc)
}

// Options carries all optional dependencies for a Model.
type Options struct {
	Provider       app.StateProvider
	ConfigProvider app.ConfigProvider
	Agent          *agent.Agent
	Renderer       *lipgloss.Renderer
	Broadcaster    *logging.Broadcaster
	// Ctx bounds the model lifetime (e.g. an SSH session context). All
	// background work started by the model is tied to it, so cancelling Ctx
	// tears the session down. Nil means context.Background().
	Ctx context.Context
	// IdleTimeout, when non-zero, quits the session after this long with no
	// key or mouse input. Zero disables the watchdog. This is a TUI-level
	// (application input) idle timeout, distinct from and complementary to
	// any transport-level idle timeout the caller may also apply — see the
	// SSH server's IdleTimeoutSeconds doc comment for why both exist.
	IdleTimeout time.Duration
}

// NewModelWithOptions creates a new TUI model with all optional dependencies.
func NewModelWithOptions(provider app.StateProvider, configProvider app.ConfigProvider, ag *agent.Agent, renderer *lipgloss.Renderer, bc *logging.Broadcaster) Model {
	return NewModelFromOptions(Options{
		Provider:       provider,
		ConfigProvider: configProvider,
		Agent:          ag,
		Renderer:       renderer,
		Broadcaster:    bc,
	})
}

// NewModelFromOptions creates a new TUI model from opts.
func NewModelFromOptions(opts Options) Model {
	provider := opts.Provider
	configProvider := opts.ConfigProvider
	ag := opts.Agent
	renderer := opts.Renderer
	bc := opts.Broadcaster

	parent := opts.Ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	var theme Theme
	if renderer != nil {
		theme = ThemeWithRenderer(renderer)
	} else {
		theme = DefaultTheme()
	}
	m := Model{
		provider:       provider,
		state:          app.AppState{}, // populated by first subscription update
		activeTab:      TabDashboard,
		keys:           DefaultKeyMap(),
		theme:          theme,
		ioNames:        make(map[string]string),
		ioPrevStates:   make(map[string]bool),
		ioStateChanged: make(map[string]time.Time),
		ioNameInput:    newIoNameInput(),
		ioImportInput:  newIoImportInput(),
		timedInput:     newTimedInput(),
		ctx:            ctx,
		cancel:         cancel,
		agent:          ag,
		chat:           NewChatView(ctx, ag, theme),
		logs:           NewLogsView(ctx, bc, theme),
		configEditor:   NewConfigEditor(configProvider, theme),
		idleTimeout:    opts.IdleTimeout,
		lastInput:      time.Now(),
	}
	if man, ok := provider.(app.IoNamesManager); ok {
		m.ioNamesMan = man
	}
	m.stateCh = provider.Subscribe(ctx, 500*time.Millisecond)

	// Disconnects, panics, and server shutdown all kill the Bubble Tea
	// program without running any Update branch, so the explicit-quit key
	// handlers are not a reliable place to release resources. The derived
	// ctx is: it is cancelled on every teardown path (the caller cancels the
	// parent, or Model.Close cancels it directly). Release the log
	// subscription here so an abandoned session doesn't keep every future
	// log line queued for it forever; the state subscription already
	// selects on this same ctx.
	logsView := m.logs
	go func() {
		<-ctx.Done()
		logsView.Close()
	}()

	return m
}

// Init initializes the model. It does not subscribe to logs — the Logs tab
// does that lazily via LogsView.Start when the user actually views it.
func (m Model) Init() tea.Cmd {
	if m.idleTimeout > 0 {
		return tea.Batch(m.waitForNextState(), idleWatchdogTick())
	}
	return m.waitForNextState()
}

// idleWatchdogInterval is how often the idle watchdog checks elapsed time
// since the last key/mouse input. It only needs to be fine-grained relative
// to the configured timeout (default 900s), not to wall-clock time, so a
// cheap 20s tick keeps the goroutine essentially free while still resolving
// a 900s timeout to within about 2%.
const idleWatchdogInterval = 20 * time.Second

// idleWatchdogTickMsg drives the idle-timeout watchdog loop (see
// Model.idleTimeout). It reschedules itself every idleWatchdogInterval for
// as long as the session stays under its idle timeout.
type idleWatchdogTickMsg struct{}

func idleWatchdogTick() tea.Cmd {
	return tea.Tick(idleWatchdogInterval, func(time.Time) tea.Msg {
		return idleWatchdogTickMsg{}
	})
}

// waitForNextState returns a command that blocks until the next state arrives
// on the shared channel, or the model's context is cancelled (session
// teardown), so a killed program never leaves this goroutine parked forever.
func (m Model) waitForNextState() tea.Cmd {
	ctx := m.ctx
	ch := m.stateCh
	return func() tea.Msg {
		select {
		case state, ok := <-ch:
			if !ok {
				return nil
			}
			return StateUpdateMsg{State: state}
		case <-ctx.Done():
			return nil
		}
	}
}

// Close tears down the model's background work: it cancels the model's
// context — stopping the state subscription and unblocking any goroutine
// waiting on it — and releases the log broadcaster subscription. Idempotent
// and safe to call concurrently (context.CancelFunc and LogsView.Close both
// are), including racing with the ctx-watcher goroutine started above.
func (m Model) Close() {
	m.cancel()
	m.logs.Close()
}

// nextTab returns the tab after the active one, wrapping around.
func (m Model) nextTab() Tab {
	return (m.activeTab + 1) % Tab(len(AllTabs()))
}

// prevTab returns the tab before the active one, wrapping around.
func (m Model) prevTab() Tab {
	if m.activeTab == 0 {
		return Tab(len(AllTabs()) - 1)
	}
	return m.activeTab - 1
}

// setActiveTab switches to tab, resetting the list cursor, focusing chat when
// entering it, and managing the log subscription lifecycle: leaving TabLogs
// unsubscribes (so an idle session on another tab does no work for every log
// line the process emits) and entering it subscribes, returning the command
// that starts the wait for the next line. The broadcaster replays its ring
// buffer to new subscribers, so re-entering Logs still shows recent history.
func (m *Model) setActiveTab(tab Tab) tea.Cmd {
	if m.activeTab == TabLogs && tab != TabLogs {
		m.logs.Stop()
	}
	var cmd tea.Cmd
	if tab == TabLogs && m.activeTab != TabLogs {
		cmd = m.logs.Start()
	}
	m.activeTab = tab
	m.cursor = 0
	if tab == TabChat {
		m.chat.Focus()
	}
	if tab == TabConfig {
		// Refresh on entry rather than waiting for the next state tick, so
		// the tab never opens showing a config the provider has moved past.
		m.configEditor.RefreshIfStale()
	}
	return cmd
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case ioExportClearMsg:
		m.ioExportMsg = ""
		m.ioImporting = false
		return m, nil

	case ioNamesResultMsg:
		m.ioExportMsg = msg.message
		return m, clearExportMsg()

	case ConfigSaveMsg:
		switch {
		case msg.Error == nil:
			m.configEditor.statusMsg = "Saved, reloading..."
			m.configEditor.dirty = false
			m.configEditor.saveConflict = false
			// Refresh the editor's own config/revision snapshot to match what
			// was just persisted (Reload is a no-op while dirty, but dirty was
			// just cleared above). This is separate from TriggerReload/the
			// app-level config reload below and elsewhere: it just keeps this
			// editor's revision from going stale on its very next edit.
			m.configEditor.Reload()
		case errors.Is(msg.Error, app.ErrConfigRevisionMismatch):
			// Do NOT clear dirty here: the user's edits are still only held
			// in ce.config and would otherwise be lost with no recovery. This
			// message fades after 3s like any other status message (see
			// configSaveClearMsg below), but that is only cosmetic:
			// saveConflict itself does not expire, so Ctrl+R/Ctrl+S still
			// opens the discard-confirmation prompt (see configDiscardConfirm)
			// whenever the user gets back to it, whether that's now or later.
			m.configEditor.statusMsg = "Config changed elsewhere since you loaded it - your edits are kept, but not saved. Press Ctrl+R/Ctrl+S again to be asked whether to discard them and load the latest version."
			m.configEditor.saveConflict = true
		default:
			m.configEditor.statusMsg = "Save error: " + msg.Error.Error()
			m.configEditor.saveConflict = false
		}
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg {
			return configSaveClearMsg{}
		})

	case configSaveClearMsg:
		m.configEditor.statusMsg = ""
		return m, nil

	case idleWatchdogTickMsg:
		if m.idleTimeout <= 0 {
			// Watchdog was disabled after being scheduled (shouldn't happen
			// in practice — idleTimeout is fixed at construction — but stop
			// rescheduling rather than looping forever if it ever does).
			return m, nil
		}
		if time.Since(m.lastInput) >= m.idleTimeout {
			m.Close()
			return m, tea.Quit
		}
		return m, idleWatchdogTick()

	case tea.MouseMsg:
		m.lastInput = time.Now()
		return m, nil

	case tea.KeyMsg:
		m.lastInput = time.Now()
		// Handle IO import mode - intercept all keys
		if m.ioImporting {
			switch msg.Type {
			case tea.KeyEnter:
				path := strings.TrimSpace(m.ioImportInput.Value())
				if path == "" {
					path = "io_names.json"
				}
				cmd := m.doImportIoNames(path)
				m.ioImporting = false
				m.ioImportInput.Blur()
				return m, cmd
			case tea.KeyEsc:
				m.ioImporting = false
				m.ioImportInput.Blur()
				return m, nil
			default:
				var cmd tea.Cmd
				m.ioImportInput, cmd = m.ioImportInput.Update(msg)
				return m, cmd
			}
		}

		// Handle IO naming mode - intercept all keys
		if m.ioNaming {
			switch msg.Type {
			case tea.KeyEnter:
				name := strings.TrimSpace(m.ioNameInput.Value())
				m.setIoName(m.ioNameTarget, name)
				m.configEditor.SetIoDisplayNames(m.ioNames)
				m.ioNaming = false
				m.ioNameInput.Blur()
				return m, nil
			case tea.KeyEsc:
				m.ioNaming = false
				m.ioNameInput.Blur()
				return m, nil
			default:
				var cmd tea.Cmd
				m.ioNameInput, cmd = m.ioNameInput.Update(msg)
				return m, cmd
			}
		}

		// Handle timed-control duration prompt - intercept all keys
		if m.timedPrompt {
			switch msg.Type {
			case tea.KeyEnter:
				cmd := m.commitTimedPrompt()
				m.timedPrompt = false
				m.timedInput.Blur()
				return m, cmd
			case tea.KeyEsc:
				m.timedPrompt = false
				m.timedInput.Blur()
				return m, nil
			default:
				var cmd tea.Cmd
				m.timedInput, cmd = m.timedInput.Update(msg)
				return m, cmd
			}
		}

		// Discard-conflict confirmation - intercept all keys. Entered only
		// from the Ctrl+S/Ctrl+R handler below when a save has already
		// failed with a revision conflict; only "y" is destructive. Enter
		// (the no-input default), "n" and Esc all cancel, leaving the dirty
		// editor and its edits untouched - see the "y/N" convention in the
		// prompt text below.
		if m.configDiscardConfirm {
			switch msg.String() {
			case "y", "Y":
				m.configDiscardConfirm = false
				m.configEditor.discardAndReload()
				m.configEditor.TriggerReload()
			default:
				m.configDiscardConfirm = false
			}
			return m, nil
		}

		// Global Ctrl+S / Ctrl+R: save (if dirty) then reload; or just reload
		// if not dirty. Ctrl+R duplicates Ctrl+S because it works over SSH
		// where ctrl+s is intercepted (XOFF) by some terminals.
		if msg.String() == "ctrl+s" || msg.String() == "ctrl+r" {
			if m.configEditor.IsDirty() {
				if m.configEditor.HasSaveConflict() {
					// A previous Save in this session already failed with a
					// stale revision; retrying with that same revision would
					// only fail again. Rather than acting immediately (which
					// is exactly the trap that lost edits before), open an
					// explicit confirmation - see the configDiscardConfirm
					// handling above.
					m.configDiscardConfirm = true
					return m, nil
				}
				return m, m.configEditor.Save()
			}
			// Not dirty: refresh this editor's own config/revision snapshot
			// (ConfigEditor.Reload) and separately ask the app to reload the
			// whole SwKit instance from disk (TriggerReload) - the two are
			// different operations that happen to both be "give me the
			// latest config" from the user's point of view. See Reload's and
			// TriggerReload's doc comments.
			//
			// TriggerReload is asynchronous, so the Reload here can only show
			// the pre-reload config; whatever the application loads moments
			// later is picked up by RefreshIfStale on a following state tick
			// rather than needing a second keypress.
			m.configEditor.Reload()
			m.configEditor.TriggerReload()
			return m, nil
		}

		// Handle config tab - forward most keys to config editor
		if m.activeTab == TabConfig {
			// Only capture tab switching and quit from config
			switch {
			case key.Matches(msg, m.keys.Tab):
				return m, m.setActiveTab(m.nextTab())
			case key.Matches(msg, m.keys.ShiftTab):
				return m, m.setActiveTab(m.prevTab())
			case key.Matches(msg, m.keys.Quit):
				m.Close()
				return m, tea.Quit
			case key.Matches(msg, m.keys.Help):
				m.showHelp = !m.showHelp
				return m, nil
			default:
				cmd := m.configEditor.Update(msg)
				return m, cmd
			}
		}

		// Handle chat tab specially - forward most keys to chat view
		if m.activeTab == TabChat && m.chat.IsFocused() {
			// Only capture tab switching from chat (let all other keys through including 'q')
			switch {
			case key.Matches(msg, m.keys.Tab):
				m.chat.Blur()
				return m, m.setActiveTab(m.nextTab())
			case key.Matches(msg, m.keys.ShiftTab):
				m.chat.Blur()
				return m, m.setActiveTab(m.prevTab())
			case msg.String() == "esc":
				m.chat.Blur()
				return m, nil
			default:
				// Forward to chat view
				var cmd tea.Cmd
				m.chat, cmd = m.chat.Update(msg)
				return m, cmd
			}
		}

		// Handle logs tab — scroll, auto-scroll toggle, clear
		if m.activeTab == TabLogs {
			switch {
			case key.Matches(msg, m.keys.Quit):
				m.Close()
				return m, tea.Quit
			case key.Matches(msg, m.keys.Tab):
				return m, m.setActiveTab(m.nextTab())
			case key.Matches(msg, m.keys.ShiftTab):
				return m, m.setActiveTab(m.prevTab())
			case key.Matches(msg, m.keys.Up):
				m.logs.ScrollUp(3)
				return m, nil
			case key.Matches(msg, m.keys.Down):
				m.logs.ScrollDown(3)
				return m, nil
			case msg.String() == "pgup" || msg.String() == "ctrl+u":
				m.logs.ScrollUp(m.logs.viewport.Height / 2)
				return m, nil
			case msg.String() == "pgdown" || msg.String() == "ctrl+d":
				m.logs.ScrollDown(m.logs.viewport.Height / 2)
				return m, nil
			case msg.String() == "a":
				m.logs.ToggleAutoScroll()
				return m, nil
			case msg.String() == "c":
				m.logs.ClearLines()
				return m, nil
			case key.Matches(msg, m.keys.Help):
				m.showHelp = !m.showHelp
				return m, nil
			}
			return m, nil
		}

		switch {
		case key.Matches(msg, m.keys.Quit):
			m.Close()
			return m, tea.Quit

		case key.Matches(msg, m.keys.Tab):
			return m, m.setActiveTab(m.nextTab())

		case key.Matches(msg, m.keys.ShiftTab):
			return m, m.setActiveTab(m.prevTab())

		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil

		case key.Matches(msg, m.keys.Down):
			maxItems := m.getMaxItems()
			if m.cursor < maxItems-1 {
				m.cursor++
			}
			return m, nil

		case key.Matches(msg, m.keys.PageUp):
			m.cursor -= m.pageStep()
			if m.cursor < 0 {
				m.cursor = 0
			}
			return m, nil

		case key.Matches(msg, m.keys.PageDown):
			m.cursor += m.pageStep()
			if maxItems := m.getMaxItems(); m.cursor > maxItems-1 {
				m.cursor = maxItems - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
			return m, nil

		case key.Matches(msg, m.keys.Help):
			m.showHelp = !m.showHelp
			return m, nil

		case key.Matches(msg, m.keys.Refresh):
			return m, m.refreshState()

		case key.Matches(msg, m.keys.Filter):
			if m.activeTab == TabIoDebug {
				m.ioDebugFilter = (m.ioDebugFilter + 1) % ioFilterCount
				m.cursor = 0
				return m, nil
			}

		case key.Matches(msg, m.keys.Name):
			if m.activeTab == TabIoDebug && m.ioDebugFilter != IoFilterAll {
				points := m.ioDebugByType(m.ioDebugFilter)
				if m.cursor >= 0 && m.cursor < len(points) {
					pt := points[m.cursor]
					k := ioPointKey(pt)
					m.ioNameTarget = k
					m.ioNameInput.SetValue(m.getIoName(k))
					m.ioNameInput.Focus()
					m.ioNaming = true
					return m, textinput.Blink
				}
			}

		case key.Matches(msg, m.keys.Export):
			if m.activeTab == TabIoDebug && m.hasIoNames() {
				return m, m.doExportIoNames()
			}

		case key.Matches(msg, m.keys.Import):
			if m.activeTab == TabIoDebug && m.ioNamesMan != nil {
				m.ioImportInput.SetValue("io_names.json")
				m.ioImportInput.Focus()
				m.ioImporting = true
				return m, textinput.Blink
			}

		case msg.String() == "+" || msg.String() == "=" || msg.String() == "-" || msg.String() == "_":
			delta := 5
			if msg.String() == "-" || msg.String() == "_" {
				delta = -5
			}
			if m.activeTab == TabIoDebug && m.ioDebugFilter == IoFilterAnalog {
				return m, m.stepSelectedIoAnalog(delta)
			}
			if m.activeTab == TabDevices {
				return m, m.stepSelectedDeviceBrightness(delta)
			}

		case msg.String() == "f":
			if m.activeTab == TabDevices && m.cursor >= 0 && m.cursor < len(m.state.Devices) {
				m.timedTarget = m.cursor
				m.timedInput.SetValue("")
				m.timedInput.Focus()
				m.timedPrompt = true
				return m, nil
			}

		case key.Matches(msg, m.keys.Enter):
			if m.activeTab == TabDevices {
				return m, m.toggleSelectedDevice()
			}
			if m.activeTab == TabIoDebug && m.ioDebugFilter == IoFilterOutputs {
				return m, m.toggleSelectedIoOutput()
			}
			if m.activeTab == TabChat && !m.chat.IsFocused() {
				m.chat.Focus()
				return m, nil
			}
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Update chat view size (account for header, tabs, help)
		chatHeight := m.height - 10
		if chatHeight < 10 {
			chatHeight = 10
		}
		m.chat.SetSize(m.width-4, chatHeight)
		m.logs.SetSize(m.width-4, chatHeight)
		return m, nil

	case ChatResponseMsg:
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case configWizardToggleMsg:
		controller, ok := m.provider.(app.DeviceController)
		if !ok {
			return m, nil
		}
		for i, d := range m.state.Devices {
			if d.Name == msg.DeviceName {
				idx := i
				return m, func() tea.Msg {
					return ControlResultMsg{Result: controller.ToggleDevice(idx)}
				}
			}
		}
		return m, nil

	case configIoToggleMsg:
		switch msg.IoType {
		case "output":
			if controller, ok := m.provider.(app.IoOutputController); ok {
				driver, index := msg.DriverName, msg.Index
				return m, func() tea.Msg {
					_ = controller.ToggleIoOutput(driver, index)
					return nil
				}
			}
		case "analog_output":
			if controller, ok := m.provider.(app.IoAnalogOutputController); ok {
				// Toggle between 0 and full scale for identification.
				target := msg.Max
				if msg.CurValue > 0 {
					target = 0
				}
				driver, index := msg.DriverName, msg.Index
				return m, func() tea.Msg {
					_ = controller.SetIoAnalogOutput(driver, index, target)
					return nil
				}
			}
		}
		return m, nil

	case LogLineMsg:
		cmd := m.logs.Update(msg)
		return m, cmd

	case StateUpdateMsg:
		m.applyState(msg.State)
		return m, m.waitForNextState()

	case RefreshStateMsg:
		// Manual refresh result (see refreshState). Unlike StateUpdateMsg this
		// does not chain another waitForNextState — that wait is already
		// pending on the long-lived subscription channel, and starting a
		// second one here would race it for the same channel receive.
		m.applyState(msg.State)
		return m, nil

	case ControlResultMsg:
		// Control operation completed - state will update on next poll
		// Could add error feedback here in the future
		return m, nil
	}

	return m, nil
}

// RefreshStateMsg carries the result of a manual state refresh triggered by
// the Refresh key (see refreshState).
type RefreshStateMsg struct {
	State app.AppState
}

// applyState updates the model and dependent views from a freshly fetched
// state snapshot, regardless of whether it arrived via the subscription
// (StateUpdateMsg) or a manual refresh (RefreshStateMsg).
func (m *Model) applyState(state app.AppState) {
	m.detectIoStateChanges(state.IoDebug)
	m.configEditor.SetIoPoints(state.IoDebug)
	if m.ioNamesMan != nil {
		m.configEditor.SetIoDisplayNames(m.ioNamesMan.GetIoNames())
	} else {
		m.configEditor.SetIoDisplayNames(m.ioNames)
	}
	m.configEditor.SetDeviceStates(state.Devices)
	// Pick up a config that changed underneath us (an out-of-band edit, or the
	// asynchronous application reload that Ctrl+R/Ctrl+S triggers completing
	// after the fact). Only while the Config tab is actually being viewed, to
	// keep the revision hash off the hot path for every other tab; entering
	// the tab refreshes too, see setActiveTab.
	if m.activeTab == TabConfig {
		m.configEditor.RefreshIfStale()
	}
	m.state = state
}

// refreshState returns a command that fetches a fresh state snapshot off the
// Update goroutine. GetState no longer holds the provider lock for the whole
// snapshot (it releases it after reading the SwKit pointer), but it still
// calls into every driver, so a slow or stuck driver would freeze keyboard
// handling and rendering for the session if this ran inline in Update.
func (m Model) refreshState() tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		return RefreshStateMsg{State: provider.GetState()}
	}
}

// commitTimedPrompt sets the targeted device on for the entered number of
// seconds (default 60 when empty/invalid), reverting afterwards.
func (m Model) commitTimedPrompt() tea.Cmd {
	controller, ok := m.provider.(app.DeviceController)
	if !ok {
		return nil
	}
	seconds := 60
	if v, err := strconv.Atoi(strings.TrimSpace(m.timedInput.Value())); err == nil && v > 0 {
		seconds = v
	}
	index := m.timedTarget
	return func() tea.Msg {
		return ControlResultMsg{Result: controller.SetDeviceValueFor(index, true, seconds)}
	}
}

// toggleSelectedDevice sends a toggle command for the currently selected device
func (m Model) toggleSelectedDevice() tea.Cmd {
	controller, ok := m.provider.(app.DeviceController)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		return ControlResultMsg{Result: controller.ToggleDevice(m.cursor)}
	}
}

// stepSelectedDeviceBrightness adjusts the brightness of the selected device by
// deltaPct (clamped 0-100). Only dimmable lights respond; other devices are ignored.
func (m Model) stepSelectedDeviceBrightness(deltaPct int) tea.Cmd {
	controller, ok := m.provider.(app.DeviceController)
	if !ok {
		return nil
	}
	if m.cursor < 0 || m.cursor >= len(m.state.Devices) {
		return nil
	}
	dev := m.state.Devices[m.cursor]
	if dev.Type != app.DeviceTypeDimmableLight {
		return nil
	}
	newPct := dev.Brightness + deltaPct
	if newPct < 0 {
		newPct = 0
	}
	if newPct > 100 {
		newPct = 100
	}
	index := m.cursor
	return func() tea.Msg {
		return ControlResultMsg{Result: controller.SetDeviceBrightness(index, newPct)}
	}
}

// toggleSelectedIoOutput sends a toggle command for the selected IO output
func (m Model) toggleSelectedIoOutput() tea.Cmd {
	controller, ok := m.provider.(app.IoOutputController)
	if !ok {
		return nil
	}
	outputs := m.ioDebugByType(IoFilterOutputs)
	if m.cursor < 0 || m.cursor >= len(outputs) {
		return nil
	}
	pt := outputs[m.cursor]
	return func() tea.Msg {
		_ = controller.ToggleIoOutput(pt.DriverName, pt.Index)
		return nil
	}
}

// stepSelectedIoAnalog adjusts the selected analog output by deltaPct percent of
// its range (continuous-slider style, +/- steps).
func (m Model) stepSelectedIoAnalog(deltaPct int) tea.Cmd {
	controller, ok := m.provider.(app.IoAnalogOutputController)
	if !ok {
		return nil
	}
	points := m.ioDebugByType(IoFilterAnalog)
	if m.cursor < 0 || m.cursor >= len(points) {
		return nil
	}
	pt := points[m.cursor]
	span := pt.Max - pt.Min
	if span <= 0 {
		return nil
	}
	// current percent of range, stepped, clamped to 0-100
	curPct := (pt.Value - pt.Min) * 100 / span
	newPct := curPct + deltaPct
	if newPct < 0 {
		newPct = 0
	}
	if newPct > 100 {
		newPct = 100
	}
	newValue := pt.Min + newPct*span/100
	return func() tea.Msg {
		_ = controller.SetIoAnalogOutput(pt.DriverName, pt.Index, newValue)
		return nil
	}
}

// ioNamesResultMsg carries the outcome of an asynchronous IO-name import or
// export (see doImportIoNames / doExportIoNames). Both operations end in the
// same "show a transient status line" behaviour, so they share one result
// message type rather than each defining its own.
type ioNamesResultMsg struct {
	message string
}

// doExportIoNames returns a command that writes named IO points to a JSON
// file off the Update goroutine. SwKitProvider.SaveIoNames calls GetState
// (which walks every driver) and does a blocking os.WriteFile, either of
// which can stall if a driver call hangs; it must not run synchronously
// inside Update or it can freeze the whole session's event loop, exactly
// like the import path below.
func (m Model) doExportIoNames() tea.Cmd {
	const filename = "io_names.json"

	man := m.ioNamesMan
	if man != nil {
		return func() tea.Msg {
			if err := man.SaveIoNames(filename); err != nil {
				return ioNamesResultMsg{message: "Export error: " + err.Error()}
			}
			return ioNamesResultMsg{message: fmt.Sprintf("Exported to %s", filename)}
		}
	}

	// Fallback: local session-only names. Snapshot the data Update currently
	// holds now (it doesn't touch drivers, but m is about to move on), and do
	// the marshal/write on the returned command like the manager path above.
	type namedPoint struct {
		Driver string `json:"driver"`
		Type   string `json:"type"`
		Index  int    `json:"index"`
		HwName string `json:"hw_name"`
		Name   string `json:"name"`
	}

	var points []namedPoint
	for _, pt := range m.state.IoDebug {
		k := ioPointKey(pt)
		if name, ok := m.ioNames[k]; ok {
			points = append(points, namedPoint{
				Driver: pt.DriverName,
				Type:   pt.Type,
				Index:  pt.Index,
				HwName: pt.Name,
				Name:   name,
			})
		}
	}

	return func() tea.Msg {
		data, err := json.MarshalIndent(points, "", "  ")
		if err != nil {
			return ioNamesResultMsg{message: "Export error: " + err.Error()}
		}
		if err := os.WriteFile(filename, data, 0644); err != nil {
			return ioNamesResultMsg{message: "Export error: " + err.Error()}
		}
		return ioNamesResultMsg{message: fmt.Sprintf("Exported %d names to %s", len(points), filename)}
	}
}

// doImportIoNames returns a command that loads names from path into the
// manager off the Update goroutine. LoadIoNames reads a file from disk (now
// capped and regular-file-only, see SwKitProvider.LoadIoNames, but still a
// blocking syscall) and must not run synchronously inside Update or it can
// stall the whole session's event loop.
func (m Model) doImportIoNames(path string) tea.Cmd {
	man := m.ioNamesMan
	return func() tea.Msg {
		if man == nil {
			return ioNamesResultMsg{message: "Import not supported"}
		}
		if err := man.LoadIoNames(path); err != nil {
			return ioNamesResultMsg{message: "Import error: " + err.Error()}
		}
		return ioNamesResultMsg{message: fmt.Sprintf("Imported names from %s", path)}
	}
}

func clearExportMsg() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return ioExportClearMsg{}
	})
}

// getMaxItems returns the max navigable items for current tab
// pageStep returns how many items a page up/down jump should move the cursor,
// roughly one visible screen of the list. Falls back to a fixed step before the
// terminal size is known.
func (m Model) pageStep() int {
	if m.height <= 0 {
		return 10
	}
	// Approximate the visible list rows: terminal height minus the fixed
	// chrome (header, tab bar, help, padding, box border ~ 10 lines).
	step := m.height - 10
	if step < 1 {
		step = 1
	}
	return step
}

func (m Model) getMaxItems() int {
	switch m.activeTab {
	case TabDrivers:
		return len(m.state.Drivers)
	case TabDevices:
		return len(m.state.Devices)
	case TabIoDebug:
		if m.ioDebugFilter == IoFilterAll {
			return 0 // side-by-side view, no cursor
		}
		return len(m.ioDebugByType(m.ioDebugFilter))
	default:
		return 0
	}
}

// detectIoStateChanges compares incoming IO point states against the last seen values.
// When a state change is detected, it records the current time in ioStateChanged.
// Both maps are reference types so mutations persist across BubbleTea model copies.
func (m Model) detectIoStateChanges(pts []app.IoPointDebugState) {
	now := time.Now()
	for _, pt := range pts {
		k := ioPointKey(pt)
		if prev, ok := m.ioPrevStates[k]; ok && prev != pt.State {
			m.ioStateChanged[k] = now
		}
		m.ioPrevStates[k] = pt.State
	}
}

// ioDebugByType returns IO debug points filtered to a specific type
func (m Model) ioDebugByType(filter IoDebugFilter) []app.IoPointDebugState {
	filterType := "input"
	switch filter {
	case IoFilterOutputs:
		filterType = "output"
	case IoFilterAnalog:
		filterType = "analog_output"
	}
	var filtered []app.IoPointDebugState
	for _, pt := range m.state.IoDebug {
		if pt.Type == filterType {
			filtered = append(filtered, pt)
		}
	}
	return filtered
}

// View renders the UI
func (m Model) View() string {
	header := m.theme.Header.Render(IconHome + " swkit - " + m.state.Name)
	top := header + "\n\n" + m.renderTabBar() + "\n\n"
	if m.configDiscardConfirm {
		top += m.theme.Error.Render("Discard your edits and load the latest config? [y/N]") + "\n\n"
	}
	bottom := "\n" + m.renderHelp()

	// Compute how many lines are available for the tab content so lists can
	// window themselves and remain scrollable instead of overflowing the
	// terminal (which scrolls the header and tab bar off the top).
	contentH := -1
	if m.height > 0 {
		contentH = m.height - lipgloss.Height(m.theme.App.Render(top+bottom))
		if contentH < 1 {
			contentH = 1
		}
	}

	// Content based on active tab
	var content string
	switch m.activeTab {
	case TabDashboard:
		content = m.renderDashboard()
	case TabDrivers:
		content = m.renderDrivers(contentH)
	case TabDevices:
		content = m.renderDevices(contentH)
	case TabConfig:
		content = m.configEditor.View(m.theme, contentH)
	case TabHomeKit:
		content = m.renderHomeKit()
	case TabIoDebug:
		content = m.renderIoDebug(contentH)
	case TabChat:
		content = m.chat.View()
	case TabLogs:
		content = m.logs.View()
	}

	// Safety net: even after per-list windowing, clamp so the full view never
	// exceeds the terminal height and crops the header/tabs off the top.
	if contentH >= 0 {
		content = clampHeight(content, contentH)
	}

	return m.theme.App.Render(top + content + bottom)
}

// clampHeight truncates s to at most max lines, keeping the top.
func clampHeight(s string, max int) string {
	if max <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	return strings.Join(lines[:max], "\n")
}

// scrollList windows a slice of single-line list entries to fit `rows`,
// keeping the item at `cursor` visible and adding "↑/↓ N more" overflow
// indicators. See windowLines for details.
func (m Model) scrollList(lines []string, cursor, rows int) string {
	return windowLines(m.theme, lines, cursor, rows)
}

// windowLines windows a slice of single-line entries to fit `rows`, keeping the
// line at index `focus` visible and adding "↑/↓ N more" overflow indicators.
// The returned block is at most `rows` lines tall. A negative `rows` (size not
// yet known) disables windowing.
func windowLines(theme Theme, lines []string, focus, rows int) string {
	if rows < 0 || len(lines) <= rows {
		return strings.Join(lines, "\n")
	}
	if rows == 0 {
		return ""
	}
	if rows <= 2 {
		// Too small for indicators; just show a window around the focus line.
		start := focus
		if start > len(lines)-rows {
			start = len(lines) - rows
		}
		if start < 0 {
			start = 0
		}
		return strings.Join(lines[start:start+rows], "\n")
	}

	// Reserve one row top and bottom for the overflow indicators.
	inner := rows - 2
	start := focus - inner/2
	if start < 0 {
		start = 0
	}
	end := start + inner
	if end > len(lines) {
		end = len(lines)
		start = end - inner
		if start < 0 {
			start = 0
		}
	}

	var out []string
	if start > 0 {
		out = append(out, theme.Muted.Render(fmt.Sprintf("   ↑ %d more", start)))
	} else {
		out = append(out, "")
	}
	out = append(out, lines[start:end]...)
	if end < len(lines) {
		out = append(out, theme.Muted.Render(fmt.Sprintf("   ↓ %d more", len(lines)-end)))
	} else {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// renderTabBar renders the navigation tabs
func (m Model) renderTabBar() string {
	var tabs []string
	for _, tab := range AllTabs() {
		style := m.theme.TabInactive
		if tab == m.activeTab {
			style = m.theme.TabActive
		}
		tabs = append(tabs, style.Render(tab.String()))
	}
	return m.theme.TabBar.Render(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
}

// renderDashboard renders the dashboard view
func (m Model) renderDashboard() string {
	summary := m.state.Summary()

	// Drivers box
	driversContent := m.theme.Primary.Render(IconDriver+" Drivers") + "\n"
	driversStatus := m.theme.Success.Render("Ready")
	if summary.DriversReady < summary.DriversTotal {
		driversStatus = m.theme.Error.Render("Issues")
	}
	driversContent += driversStatus + " " + m.theme.Secondary.Render(
		strings.Repeat("●", summary.DriversReady)+
			strings.Repeat("○", summary.DriversTotal-summary.DriversReady))
	driversBox := m.theme.Box.Width(24).Render(driversContent)

	// Devices box
	devicesContent := m.theme.Primary.Render(IconHome+" Devices") + "\n"
	devicesContent += IconLight + " Lights: " + m.theme.Secondary.Render(itoa(summary.LightsCount)) + "\n"
	if summary.ColorLightsCount > 0 {
		devicesContent += IconColorLight + " Color: " + m.theme.Secondary.Render(itoa(summary.ColorLightsCount)) + "\n"
	}
	if summary.DimmableLightsCount > 0 {
		devicesContent += IconLight + " Dimmable: " + m.theme.Secondary.Render(itoa(summary.DimmableLightsCount)) + "\n"
	}
	devicesContent += IconOutlet + " Outlets: " + m.theme.Secondary.Render(itoa(summary.OutletsCount)) + "\n"
	devicesContent += IconButton + " Buttons: " + m.theme.Secondary.Render(itoa(summary.ButtonsCount))
	devicesBox := m.theme.Box.Width(24).Render(devicesContent)

	// HomeKit box
	hkContent := m.theme.Primary.Render(IconHomeKit+" HomeKit") + "\n"
	if summary.HomeKitEnabled {
		hkContent += m.theme.Success.Render("Enabled") + "\n"
		hkContent += m.theme.Secondary.Render(itoa(summary.HomeKitDevices) + " accessories")
	} else {
		hkContent += m.theme.Muted.Render("Disabled")
	}
	hkBox := m.theme.Box.Width(24).Render(hkContent)

	// Layout boxes
	leftColumn := lipgloss.JoinVertical(lipgloss.Left, driversBox, hkBox)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftColumn, "  ", devicesBox)
}

// renderDrivers renders the drivers view
func (m Model) renderDrivers(height int) string {
	if len(m.state.Drivers) == 0 {
		return m.theme.Muted.Render("No drivers configured")
	}

	var lines []string
	for i, driver := range m.state.Drivers {
		prefix := "   "
		style := m.theme.ListItem
		if i == m.cursor {
			prefix = " ▶ "
			style = m.theme.ListItemSelected
		}

		statusText := m.theme.Ready.Render("Ready")
		if !driver.Ready {
			statusText = m.theme.NotReady.Render("Not Ready")
		}

		line := prefix + m.theme.Primary.Render(padRight(driver.Name, 12)) + " " + statusText
		if driver.StatusInfo != "" {
			line += "  " + m.theme.Secondary.Render(driver.StatusInfo)
		}

		lines = append(lines, style.Render(line))
	}

	content := m.scrollList(lines, m.cursor, boxRows(height))
	return m.theme.Box.Render(content)
}

// boxRows returns the number of content rows available inside a single bordered
// box given the total height budget (subtracting the top and bottom border).
// A negative budget (unknown size) is passed through to disable windowing.
func boxRows(height int) int {
	if height < 0 {
		return -1
	}
	rows := height - 2
	if rows < 1 {
		rows = 1
	}
	return rows
}

// renderDevices renders the devices view with list and detail panel
func (m Model) renderDevices(height int) string {
	if len(m.state.Devices) == 0 {
		return m.theme.Muted.Render("No devices configured")
	}

	rows := boxRows(height)
	listBox := m.theme.Box.Render(m.renderDeviceList(rows))
	// Keep the detail panel within the same height budget so the row block
	// (whose height follows the taller side) never overflows the screen.
	detail := m.renderDeviceDetail()
	if rows >= 0 {
		detail = clampHeight(detail, rows)
	}
	detailBox := m.theme.Box.Width(40).Render(detail)

	result := lipgloss.JoinHorizontal(lipgloss.Top, listBox, " ", detailBox)
	if m.timedPrompt {
		name := ""
		if m.timedTarget >= 0 && m.timedTarget < len(m.state.Devices) {
			name = m.state.Devices[m.timedTarget].Name
		}
		result += "\n" + m.theme.Secondary.Render("Turn "+name+" on for (seconds): ") + m.timedInput.View()
	}
	return result
}

// renderDeviceList renders the device list for the left column
func (m Model) renderDeviceList(rows int) string {
	var lines []string
	for i, device := range m.state.Devices {
		prefix := "   "
		style := m.theme.ListItem
		if i == m.cursor {
			prefix = " ▶ "
			style = m.theme.ListItemSelected
		}

		icon := deviceIcon(device.Type)

		// State indicator
		stateText := m.theme.Off.Render(IconOff)
		if device.Type != app.DeviceTypeButton && device.IsOn {
			stateText = m.theme.On.Render(IconOn)
		}
		if device.Type == app.DeviceTypeButton {
			now := m.state.Timestamp
			if !device.LastEventTime.IsZero() && now.Sub(device.LastEventTime) < 2*time.Second {
				stateText = m.theme.Event.Render(IconOn)
			} else if !device.LastEventTime.IsZero() && now.Sub(device.LastEventTime) < 200*time.Second {
				stateText = m.theme.On.Render(IconOn)
			} else {
				stateText = m.theme.Secondary.Render("-")
			}
		}

		// Health indicator
		healthText := ""
		if device.IsFaulty {
			healthText = m.theme.Faulty.Render(" " + IconFaulty)
		}

		// HomeKit indicator
		hkText := ""
		if !device.HomeKitEnabled {
			hkText = m.theme.Muted.Render(" (no HK)")
		}

		// Activity timer for buttons (counts up for 200s after last event)
		eventTimerText := ""
		if device.Type == app.DeviceTypeButton && !device.LastEventTime.IsZero() {
			ago := m.state.Timestamp.Sub(device.LastEventTime)
			if ago < 200*time.Second {
				eventTimerText = " " + m.theme.On.Render(fmt.Sprintf("%ds", int(ago.Seconds())))
			}
		}

		// Brightness for dimmable lights
		brightnessText := ""
		if device.Type == app.DeviceTypeDimmableLight {
			brightnessText = " " + m.theme.Secondary.Render(itoa(device.Brightness)+"%")
		}

		line := prefix + icon + " " + m.theme.Primary.Render(padRight(device.Name, 20)) +
			" " + stateText + brightnessText + eventTimerText + healthText + hkText

		lines = append(lines, style.Render(line))
	}

	return m.scrollList(lines, m.cursor, rows)
}

// renderDeviceDetail renders the detail panel for the selected device
func (m Model) renderDeviceDetail() string {
	if m.cursor < 0 || m.cursor >= len(m.state.Devices) {
		return m.theme.Muted.Render("No device selected")
	}

	device := m.state.Devices[m.cursor]
	icon := deviceIcon(device.Type)

	// Title
	title := m.theme.Primary.Render(icon + " " + device.Name)

	// Type
	typeName := string(device.Type)
	typeLabel := m.theme.Secondary.Render("Type: ") + typeName

	// HomeKit status
	hkLabel := m.theme.Secondary.Render("HomeKit: ")
	if device.HomeKitEnabled {
		hkLabel += m.theme.Success.Render("enabled")
	} else {
		hkLabel += m.theme.Muted.Render("disabled")
	}

	// Health status
	healthLabel := m.theme.Secondary.Render("Health: ")
	if device.IsFaulty {
		healthLabel += m.theme.Faulty.Render("faulty")
	} else if device.IsHealthy {
		healthLabel += m.theme.Healthy.Render("healthy")
	} else {
		healthLabel += m.theme.Muted.Render("unknown")
	}

	lines := []string{title, "", typeLabel, hkLabel, healthLabel}

	if device.Type == app.DeviceTypeDimmableLight {
		lines = append(lines, m.theme.Secondary.Render("Brightness: ")+itoa(device.Brightness)+"%")
	}

	// IO Config section
	hasIo := device.OutputIoId != "" || device.RgbwIoId != "" || device.AnalogIoId != "" || device.EventInputId != ""
	if hasIo {
		lines = append(lines, "", m.theme.BoxTitle.Render("IO Config"))
		if device.OutputIoId != "" {
			lines = append(lines, m.theme.Secondary.Render("Output: ")+device.OutputIoId)
		}
		if device.RgbwIoId != "" {
			lines = append(lines, m.theme.Secondary.Render("RGBW:   ")+device.RgbwIoId)
		}
		if device.AnalogIoId != "" {
			lines = append(lines, m.theme.Secondary.Render("Analog: ")+device.AnalogIoId)
		}
		if device.EventInputId != "" {
			lines = append(lines, m.theme.Secondary.Render("Input:  ")+device.EventInputId)
		}
	}

	// Last event (buttons only)
	if device.Type == app.DeviceTypeButton {
		lines = append(lines, "", m.theme.BoxTitle.Render("Last Event"))
		if device.LastEventTime.IsZero() {
			lines = append(lines, m.theme.Muted.Render("  no event since startup"))
		} else {
			now := m.state.Timestamp
			ago := now.Sub(device.LastEventTime)
			agoStr := fmt.Sprintf("%ds ago", int(ago.Seconds()))
			if ago >= time.Minute {
				agoStr = fmt.Sprintf("%dm%ds ago", int(ago.Minutes()), int(ago.Seconds())%60)
			}
			eventStyle := m.theme.On
			if ago < 2*time.Second {
				eventStyle = m.theme.Event
			}
			lines = append(lines, "  "+eventStyle.Render(device.LastEventType)+"  "+m.theme.Secondary.Render(agoStr))
		}
	}

	// Controls section (buttons only)
	if len(device.ControlRelations) > 0 {
		lines = append(lines, "", m.theme.BoxTitle.Render("Controls"))
		for _, rel := range device.ControlRelations {
			action := rel.Action
			if app.IsBrightnessVerb(rel.Action) {
				action += " " + brightnessLevelLabel(rel.Action, rel.Level)
			}
			lines = append(lines, m.theme.Secondary.Render(rel.EventType)+" "+
				m.theme.On.Render(action)+" "+
				m.theme.Primary.Render(rel.DeviceName))
		}
	}

	return strings.Join(lines, "\n")
}

// renderHomeKit renders the HomeKit view
func (m Model) renderHomeKit() string {
	hk := m.state.HomeKit

	var content string
	if hk.Enabled {
		content = m.theme.Success.Render("HomeKit Enabled") + "\n\n"
		content += m.theme.Secondary.Render("PIN: ") + m.theme.Primary.Render(formatPin(hk.Pin)) + "\n"
		if hk.Address != "" {
			content += m.theme.Secondary.Render("Address: ") + m.theme.Primary.Render(hk.Address) + "\n"
		}
		content += m.theme.Secondary.Render("Accessories: ") + m.theme.Primary.Render(itoa(hk.DeviceCount))
	} else {
		content = m.theme.Muted.Render("HomeKit is not configured\n")
		content += m.theme.Secondary.Render("Set HkPin in config.json to enable")
	}

	return m.theme.Box.Width(40).Render(content)
}

// renderIoDebug renders the IO debug view
func (m Model) renderIoDebug(height int) string {
	if len(m.state.IoDebug) == 0 {
		return m.theme.Muted.Render("No IO debug data available")
	}

	// Reserve rows for the chrome around the IO point lists: the filter header,
	// any active text inputs, and the export status message.
	reserved := 1 // filter header
	if m.ioNaming {
		reserved++
	}
	if m.ioImporting {
		reserved++
	}
	if m.ioExportMsg != "" {
		reserved++
	}
	listHeight := height
	if listHeight >= 0 {
		listHeight -= reserved
		if listHeight < 1 {
			listHeight = 1
		}
	}

	// Filter header
	header := m.theme.Secondary.Render("View: ")
	header += m.theme.Primary.Render(m.ioDebugFilter.String())
	header += m.theme.Muted.Render("  (f to cycle)")
	if m.ioDebugFilter != IoFilterAll {
		hint := "  n: name  w: export"
		if m.ioNamesMan != nil {
			hint += "  i: import"
		}
		if m.ioDebugFilter == IoFilterOutputs {
			hint += "  enter: toggle"
		}
		if m.ioDebugFilter == IoFilterAnalog {
			hint += "  +/-: set"
		}
		header += m.theme.Muted.Render(hint)
	}

	var result string
	if m.ioDebugFilter == IoFilterAll {
		result = header + "\n" + m.renderIoDebugColumns(listHeight)
	} else {
		points := m.ioDebugByType(m.ioDebugFilter)
		if len(points) == 0 {
			result = header + "\n" + m.theme.Muted.Render("No matching IO points")
		} else {
			content := m.scrollList(m.ioDebugLines(points, true), m.cursor, boxRows(listHeight))
			result = header + "\n" + m.theme.Box.Render(content)
		}
	}

	// Text input for naming
	if m.ioNaming {
		result += "\n" + m.theme.Secondary.Render("Name: ") + m.ioNameInput.View()
	}

	// Text input for import filename
	if m.ioImporting {
		result += "\n" + m.theme.Secondary.Render("Import file: ") + m.ioImportInput.View()
	}

	// Export/import status message
	if m.ioExportMsg != "" {
		result += "\n" + m.theme.Success.Render(m.ioExportMsg)
	}

	return result
}

// renderIoDebugColumns renders inputs and outputs side by side
func (m Model) renderIoDebugColumns(height int) string {
	inputs := m.ioDebugByType(IoFilterInputs)
	outputs := m.ioDebugByType(IoFilterOutputs)

	inputTitle := m.theme.BoxTitle.Render("Inputs (" + itoa(len(inputs)) + ")")
	outputTitle := m.theme.BoxTitle.Render("Outputs (" + itoa(len(outputs)) + ")")

	// One row of each box is the title; the rest is available for the list.
	rows := boxRows(height)
	if rows >= 0 {
		rows--
		if rows < 1 {
			rows = 1
		}
	}

	inputContent := m.theme.Muted.Render("none")
	if len(inputs) > 0 {
		// No cursor in the side-by-side view; window from the top.
		inputContent = m.scrollList(m.ioDebugLines(inputs, false), 0, rows)
	}

	outputContent := m.theme.Muted.Render("none")
	if len(outputs) > 0 {
		outputContent = m.scrollList(m.ioDebugLines(outputs, false), 0, rows)
	}

	inputBox := m.theme.Box.Render(inputTitle + "\n" + inputContent)
	outputBox := m.theme.Box.Render(outputTitle + "\n" + outputContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, inputBox, "  ", outputBox)
}

// ioDebugLines renders a list of IO points into one styled line per point.
func (m Model) ioDebugLines(points []app.IoPointDebugState, withCursor bool) []string {
	now := m.state.Timestamp
	nameWidth := 8
	for _, pt := range points {
		if len(pt.Name) > nameWidth {
			nameWidth = len(pt.Name)
		}
	}
	var lines []string
	for i, pt := range points {
		prefix := "   "
		style := m.theme.ListItem
		if withCursor && i == m.cursor {
			prefix = " ▶ "
			style = m.theme.ListItemSelected
		}

		// State indicator — orange for 2s after an explicit event, then back to normal.
		// Analog outputs show their value and percent-of-range instead of on/off.
		var stateText string
		if pt.Type == "analog_output" {
			span := pt.Max - pt.Min
			pct := 0
			if span > 0 {
				pct = (pt.Value - pt.Min) * 100 / span
			}
			stateText = m.theme.On.Render(fmt.Sprintf("%d (%d%%)", pt.Value, pct))
		} else {
			stateText = m.theme.Off.Render(IconOff)
			if pt.State {
				stateText = m.theme.On.Render(IconOn)
			}
			if !pt.LastEvent.IsZero() && now.Sub(pt.LastEvent) < 2*time.Second {
				stateText = m.theme.Event.Render(IconOn)
			}
		}

		// Health indicator
		healthText := m.theme.Healthy.Render(IconHealthy)
		if !pt.Healthy {
			healthText = m.theme.Faulty.Render(IconFaulty)
		}

		// Activity timer: counts up (seconds since last state change or event), visible for 200s
		changedText := ""
		activityTime := pt.LastChanged
		if !pt.LastEvent.IsZero() && pt.LastEvent.After(activityTime) {
			activityTime = pt.LastEvent
		}
		if tuiChange, ok := m.ioStateChanged[ioPointKey(pt)]; ok && tuiChange.After(activityTime) {
			activityTime = tuiChange
		}
		if !activityTime.IsZero() {
			const timerDuration = 200 * time.Second
			ago := now.Sub(activityTime)
			if ago < timerDuration {
				changedText = " " + m.theme.On.Render(fmt.Sprintf("%ds", int(ago.Seconds())))
			}
		}

		// Configured device name
		configuredText := ""
		if pt.ConfiguredAs != "" {
			configuredText = " " + m.theme.Muted.Render("<"+pt.ConfiguredAs+">")
		}

		// Custom name (from state, populated by provider)
		nameText := ""
		if pt.CustomName != "" {
			nameText = " " + m.theme.Secondary.Render("["+pt.CustomName+"]")
		}

		line := prefix +
			m.theme.Primary.Render(padRight(pt.Name, nameWidth)) + " " +
			stateText + " " +
			healthText +
			changedText +
			configuredText +
			nameText

		lines = append(lines, style.Render(line))
	}

	return lines
}

// renderHelp renders the help bar
func (m Model) renderHelp() string {
	if m.activeTab == TabConfig {
		configHelp := m.configEditor.ConfigHelpKeys(m.theme)
		return m.theme.Help.Render(configHelp)
	}
	if m.activeTab == TabLogs {
		scrollStatus := "off"
		if m.logs.autoScroll {
			scrollStatus = "on"
		}
		logsHelp := m.theme.HelpKey.Render("↑↓") + " " + m.theme.HelpDesc.Render("scroll") +
			"  " + m.theme.HelpKey.Render("pgup/pgdn") + " " + m.theme.HelpDesc.Render("half page") +
			"  " + m.theme.HelpKey.Render("a") + " " + m.theme.HelpDesc.Render("auto-scroll ["+scrollStatus+"]") +
			"  " + m.theme.HelpKey.Render("c") + " " + m.theme.HelpDesc.Render("clear") +
			"  " + m.theme.HelpKey.Render("tab") + " " + m.theme.HelpDesc.Render("next tab") +
			"  " + m.theme.HelpKey.Render("q") + " " + m.theme.HelpDesc.Render("quit")
		return m.theme.Help.Render(logsHelp)
	}
	bindings := m.keys.ShortHelp()
	var parts []string
	for _, b := range bindings {
		parts = append(parts, m.theme.HelpKey.Render(b.Help().Key)+" "+m.theme.HelpDesc.Render(b.Help().Desc))
	}
	// Surface brightness control when a dimmable light is selected on the Devices tab.
	if m.activeTab == TabDevices && m.cursor >= 0 && m.cursor < len(m.state.Devices) &&
		m.state.Devices[m.cursor].Type == app.DeviceTypeDimmableLight {
		parts = append(parts, m.theme.HelpKey.Render("+/-")+" "+m.theme.HelpDesc.Render("brightness"))
	}
	// Surface the timed-control key on the Devices tab.
	if m.activeTab == TabDevices && len(m.state.Devices) > 0 {
		parts = append(parts, m.theme.HelpKey.Render("f")+" "+m.theme.HelpDesc.Render("on for…"))
	}
	// Surface the page up/down keys on tabs that have a scrollable list.
	if m.getMaxItems() > 0 {
		parts = append(parts, m.theme.HelpKey.Render("ctrl+u/d")+" "+m.theme.HelpDesc.Render("page"))
	}
	return m.theme.Help.Render(strings.Join(parts, "  "))
}

// Helper functions

func deviceIcon(t app.DeviceType) string {
	switch t {
	case app.DeviceTypeLight:
		return IconLight
	case app.DeviceTypeColorLight:
		return IconColorLight
	case app.DeviceTypeDimmableLight:
		return IconLight
	case app.DeviceTypeOutlet:
		return IconOutlet
	case app.DeviceTypeButton:
		return IconButton
	case app.DeviceTypeScene:
		return IconScene
	default:
		return "?"
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}

// ioExportClearMsg clears the export status message after a delay
type ioExportClearMsg struct{}

// configSaveClearMsg clears the config save status message after a delay
type configSaveClearMsg struct{}

// configWizardToggleMsg is sent by the wizard to toggle a device for identification
type configWizardToggleMsg struct {
	DeviceName string
}

// configIoToggleMsg is sent by the IO picker to toggle a raw IO output for
// identification (e.g. blinking the physical output while choosing it).
type configIoToggleMsg struct {
	DriverName string
	Index      int
	IoType     string // "output" or "analog_output"
	CurValue   int    // analog only: current value
	Max        int    // analog only: range maximum
}

func newIoNameInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "Enter name..."
	ti.CharLimit = 40
	ti.Width = 30
	return ti
}

func newIoImportInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "io_names.json"
	ti.SetValue("io_names.json")
	ti.CharLimit = 120
	ti.Width = 40
	return ti
}

func newTimedInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "60"
	ti.CharLimit = 6
	ti.Width = 8
	return ti
}

// getIoName returns the custom name for a key, using the manager when available.
func (m Model) getIoName(key string) string {
	if m.ioNamesMan != nil {
		return m.ioNamesMan.GetIoName(key)
	}
	return m.ioNames[key]
}

// setIoName sets or deletes a custom name for a key.
func (m *Model) setIoName(key, name string) {
	if m.ioNamesMan != nil {
		m.ioNamesMan.SetIoName(key, name)
		return
	}
	if name == "" {
		delete(m.ioNames, key)
	} else {
		m.ioNames[key] = name
	}
}

// hasIoNames returns true if at least one custom name is set.
func (m Model) hasIoNames() bool {
	if m.ioNamesMan != nil {
		return len(m.ioNamesMan.GetIoNames()) > 0
	}
	return len(m.ioNames) > 0
}

// ioPointKey returns a unique key for an IO point (used as map key for names)
func ioPointKey(pt app.IoPointDebugState) string {
	return pt.DriverName + "|" + pt.Type + "|" + itoa(pt.Index)
}

func formatPin(pin string) string {
	if len(pin) == 8 {
		return pin[:3] + "-" + pin[3:5] + "-" + pin[5:]
	}
	return pin
}

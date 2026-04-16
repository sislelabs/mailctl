package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/flow"
	"github.com/sislelabs/mailctl/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Views ───────────────────────────────────────────────────────────────────

type View int

const (
	ViewInit View = iota
	ViewList
	ViewDetail
	ViewAliases
	ViewAddDomain
	ViewAddAlias
	ViewDeleteConfirm
	ViewFlows
	ViewFlowOutput
)

// ── App Model ───────────────────────────────────────────────────────────────

type App struct {
	cfg    *internal.Config
	width  int
	height int

	view     View
	prevView View

	// Sub-models
	initWizard  InitModel
	domainList  ListModel
	detail      DetailModel
	aliases     AliasesModel
	addDomain   AddDomainModel
	addAlias    AddAliasModel
	deleteConf  DeleteConfirmModel
	flowsList   FlowsModel
	flowOutput  FlowOutputModel

	// State
	statusMsg string
	err       error
}

// ── Messages ────────────────────────────────────────────────────────────────

type ConfigSavedMsg struct{ Cfg *internal.Config }
type SwitchViewMsg struct{ View View }
type StatusMsg struct{ Text string }
type RefreshMsg struct{}
type DomainSelectedMsg struct{ Domain string }
type DomainAddedMsg struct{ Domain string }
type DomainDeletedMsg struct{ Domain string }
type AliasAddedMsg struct{ Domain, Alias string }
type AliasDeletedMsg struct{ Domain, Alias string }

func NewApp() App {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return App{
			cfg:        &internal.Config{},
			view:       ViewInit,
			initWizard: NewInitModel(),
			width:      80,
			height:     24,
		}
	}

	app := App{
		cfg:    cfg,
		view:   ViewList,
		width:  80,
		height: 24,
	}
	app.domainList = NewListModel(cfg)
	return app
}

func (a App) Init() tea.Cmd {
	switch a.view {
	case ViewInit:
		return a.initWizard.Init()
	case ViewList:
		return a.domainList.Init()
	}
	return nil
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		return a.propagateUpdate(msg)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return a, tea.Quit
		}

	case addBatchMsg:
		var cmds []tea.Cmd
		for _, m := range msg.msgs {
			var cmd tea.Cmd
			a, cmd = a.propagateUpdate(m)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return a, tea.Batch(cmds...)

	case ConfigSavedMsg:
		a.cfg = msg.Cfg
		a.view = ViewList
		a.domainList = NewListModel(a.cfg)
		a.statusMsg = "Config saved"
		return a, a.domainList.Init()

	case SwitchViewMsg:
		return a.switchView(msg.View)

	case DomainSelectedMsg:
		a.view = ViewDetail
		a.detail = NewDetailModel(a.cfg, msg.Domain, a.width, a.height-4)
		return a, a.detail.Init()

	case DomainAddedMsg:
		if cfg, err := internal.LoadConfig(); err == nil {
			a.cfg = cfg
		}
		a.view = ViewList
		a.domainList = NewListModel(a.cfg)
		a.statusMsg = msg.Domain + " added"
		return a, a.domainList.Init()

	case DomainDeletedMsg:
		if cfg, err := internal.LoadConfig(); err == nil {
			a.cfg = cfg
		}
		a.view = ViewList
		a.domainList = NewListModel(a.cfg)
		a.statusMsg = msg.Domain + " deleted"
		return a, a.domainList.Init()

	case AliasAddedMsg:
		if cfg, err := internal.LoadConfig(); err == nil {
			a.cfg = cfg
		}
		a.view = ViewAliases
		d := a.cfg.FindDomain(msg.Domain)
		if d != nil {
			a.aliases = NewAliasesModel(a.cfg, d)
		}
		a.statusMsg = msg.Alias + " added"
		return a, nil

	case AliasDeletedMsg:
		if cfg, err := internal.LoadConfig(); err == nil {
			a.cfg = cfg
		}
		d := a.cfg.FindDomain(msg.Domain)
		if d != nil {
			a.aliases = NewAliasesModel(a.cfg, d)
		}
		a.statusMsg = msg.Alias + " deleted"
		return a, nil

	case RefreshMsg:
		return a.propagateUpdate(msg)

	case StatusMsg:
		a.statusMsg = msg.Text
		return a, nil

	case FlowSelectedMsg:
		a.statusMsg = "Run: mailctl flow run " + msg.Name
		return a, nil

	case FlowRunRequestMsg:
		f := flow.Get(msg.Name)
		if f == nil {
			a.statusMsg = "Flow not found: " + msg.Name
			return a, nil
		}
		// Flows with args are handled by FlowRunWithArgsMsg (after arg collection in flows view)
		// Interactive flows without args use subprocess
		if f.Def.IsInteractive() {
			return a, runFlowSubprocess(f.Name, nil)
		}
		// Non-interactive, no args — run inline
		a.view = ViewFlowOutput
		a.flowOutput = NewFlowOutputModel(msg.Name, a.cfg)
		return a, a.flowOutput.Init()

	case FlowRunWithArgsMsg:
		if msg.Subprocess {
			return a, runFlowSubprocess(msg.Name, msg.Args)
		}
		// Run inline with args
		a.view = ViewFlowOutput
		a.flowOutput = NewFlowOutputModelWithArgs(msg.Name, a.cfg, msg.Args)
		return a, a.flowOutput.Init()

	case FlowRunDoneMsg:
		// Subprocess flow finished — refresh and go back to flows
		if cfg, err := internal.LoadConfig(); err == nil {
			a.cfg = cfg
		}
		a.view = ViewFlows
		a.flowsList = NewFlowsModel()
		a.statusMsg = msg.Name + " completed"
		return a, a.flowsList.Init()
	}

	return a.propagateUpdate(msg)
}

func (a App) propagateUpdate(msg tea.Msg) (App, tea.Cmd) {
	var cmd tea.Cmd
	switch a.view {
	case ViewInit:
		var m tea.Model
		m, cmd = a.initWizard.Update(msg)
		a.initWizard = m.(InitModel)
	case ViewList:
		var m tea.Model
		m, cmd = a.domainList.Update(msg)
		a.domainList = m.(ListModel)
	case ViewDetail:
		var m tea.Model
		m, cmd = a.detail.Update(msg)
		a.detail = m.(DetailModel)
	case ViewAliases:
		var m tea.Model
		m, cmd = a.aliases.Update(msg)
		a.aliases = m.(AliasesModel)
	case ViewAddDomain:
		var m tea.Model
		m, cmd = a.addDomain.Update(msg)
		a.addDomain = m.(AddDomainModel)
	case ViewAddAlias:
		var m tea.Model
		m, cmd = a.addAlias.Update(msg)
		a.addAlias = m.(AddAliasModel)
	case ViewDeleteConfirm:
		var m tea.Model
		m, cmd = a.deleteConf.Update(msg)
		a.deleteConf = m.(DeleteConfirmModel)
	case ViewFlows:
		var m tea.Model
		m, cmd = a.flowsList.Update(msg)
		a.flowsList = m.(FlowsModel)
	case ViewFlowOutput:
		var m tea.Model
		m, cmd = a.flowOutput.Update(msg)
		a.flowOutput = m.(FlowOutputModel)
	}
	return a, cmd
}

func (a App) switchView(v View) (App, tea.Cmd) {
	a.prevView = a.view
	a.view = v
	a.statusMsg = ""

	switch v {
	case ViewList:
		a.domainList = NewListModel(a.cfg)
		return a, a.domainList.Init()
	case ViewAliases:
		domain := a.domainList.SelectedDomain()
		d := a.cfg.FindDomain(domain)
		if d == nil {
			a.statusMsg = "No domain selected"
			a.view = a.prevView
			return a, nil
		}
		a.aliases = NewAliasesModel(a.cfg, d)
		return a, nil
	case ViewAddDomain:
		a.addDomain = NewAddDomainModel(a.cfg)
		return a, a.addDomain.Init()
	case ViewAddAlias:
		domain := ""
		if a.prevView == ViewAliases {
			domain = a.aliases.domain
		} else {
			domain = a.domainList.SelectedDomain()
		}
		a.addAlias = NewAddAliasModel(a.cfg, domain)
		return a, a.addAlias.Init()
	case ViewDeleteConfirm:
		domain := a.domainList.SelectedDomain()
		if domain == "" {
			a.statusMsg = "No domain selected"
			a.view = a.prevView
			return a, nil
		}
		a.deleteConf = NewDeleteConfirmModel(a.cfg, domain)
		return a, a.deleteConf.Init()
	case ViewFlows:
		a.flowsList = NewFlowsModel()
		return a, a.flowsList.Init()
	}
	return a, nil
}

func (a App) View() string {
	cw := ui.ContentWidth(a.width)

	// ── Footer (two lines: breadcrumb + keybinds) ──
	breadcrumb := a.footerContext()
	keybinds := a.styledKeybinds()

	line1 := lipgloss.NewStyle().Width(cw).Render(breadcrumb)
	line2 := lipgloss.NewStyle().Width(cw).Render(keybinds)

	footer := ui.Centered(a.width, line1) + "\n" + ui.Centered(a.width, line2)

	// ── Content ──
	contentHeight := a.height - 3
	if contentHeight < 1 {
		contentHeight = 1
	}

	var content string
	switch a.view {
	case ViewInit:
		content = a.initWizard.View()
	case ViewList:
		content = a.domainList.View()
	case ViewDetail:
		content = a.detail.View()
	case ViewAliases:
		content = a.aliases.View()
	case ViewAddDomain:
		content = a.addDomain.View()
	case ViewAddAlias:
		content = a.addAlias.View()
	case ViewDeleteConfirm:
		content = a.deleteConf.View()
	case ViewFlows:
		content = a.flowsList.View()
	case ViewFlowOutput:
		content = a.flowOutput.View()
	}

	// Center the content block
	contentCentered := ui.Centered(a.width, lipgloss.NewStyle().Width(cw).Render(content))

	contentBox := lipgloss.NewStyle().
		Height(contentHeight).
		Width(a.width).
		Render(contentCentered)

	return contentBox + "\n" + footer
}

func (a App) footerContext() string {
	// Left side: breadcrumb path
	sep := ui.Dim.Render(" › ")

	path := ui.Accent.Render("mailctl")
	switch a.view {
	case ViewInit:
		path += sep + ui.Muted.Render("setup")
	case ViewList:
		path += sep + ui.Muted.Render("domains")
		if a.statusMsg != "" {
			path += "  " + ui.IconSuccess + " " + ui.Success.Render(a.statusMsg)
		}
	case ViewDetail:
		path += sep + ui.Muted.Render("domains") + sep + ui.White.Render(a.detail.domain)
	case ViewAliases:
		path += sep + ui.Muted.Render(a.aliases.domain) + sep + ui.White.Render("aliases")
		if a.statusMsg != "" {
			path += "  " + ui.IconSuccess + " " + ui.Success.Render(a.statusMsg)
		}
	case ViewAddDomain:
		path += sep + ui.Muted.Render("add domain")
	case ViewAddAlias:
		path += sep + ui.Muted.Render("add alias")
	case ViewDeleteConfirm:
		path += sep + ui.Error.Render("delete")
	case ViewFlows:
		path += sep + ui.Muted.Render("flows")
	case ViewFlowOutput:
		path += sep + ui.Muted.Render("flows") + sep + ui.White.Render(a.flowOutput.flowName)
	}

	if a.view == ViewList && len(a.cfg.Domains) > 0 {
		path += "  " + ui.Dim.Render(fmt.Sprintf("(%d)", len(a.cfg.Domains)))
	}

	return path
}

func (a App) styledKeybinds() string {
	kb := ui.KeyBind

	switch a.view {
	case ViewInit:
		return kb("enter", "next") + "  " + kb("⇧tab", "back") + "  " + kb("esc", "quit")
	case ViewList:
		if a.domainList.filtering {
			return kb("enter", "apply") + "  " + kb("esc", "clear")
		}
		return kb("enter", "inspect") + "  " + kb("a", "add") + "  " + kb("d", "del") + "  " + kb("tab", "aliases") + "  " + kb("f", "flows") + "  " + kb("r", "refresh") + "  " + kb("/", "filter") + "  " + kb("q", "quit")
	case ViewDetail:
		return kb("r", "refresh") + "  " + kb("esc", "back") + "  " + kb("q", "quit")
	case ViewAliases:
		return kb("a", "add") + "  " + kb("d", "delete") + "  " + kb("esc", "back") + "  " + kb("q", "quit")
	case ViewAddDomain:
		return kb("enter", "next") + "  " + kb("esc", "cancel")
	case ViewAddAlias:
		return kb("enter", "confirm") + "  " + kb("esc", "cancel")
	case ViewDeleteConfirm:
		return kb("enter", "confirm") + "  " + kb("esc", "cancel")
	case ViewFlows:
		return kb("enter", "details") + "  " + kb("r", "run") + "  " + kb("esc", "back") + "  " + kb("q", "quit")
	case ViewFlowOutput:
		if a.flowOutput.done {
			return kb("esc", "back") + "  " + kb("q", "quit")
		}
		return ui.Dim.Render("running...")
	}
	return kb("q", "quit")
}

// runFlowSubprocess suspends the TUI and runs a flow in a real terminal.
func runFlowSubprocess(name string, args map[string]string) tea.Cmd {
	exe, err := os.Executable()
	if err != nil {
		exe = "mailctl"
	}

	cmdParts := []string{exe, "flow", "run", name}
	for _, v := range args {
		cmdParts = append(cmdParts, v)
	}
	cmdStr := strings.Join(cmdParts, " ")

	script := fmt.Sprintf(`%s; echo ""; echo "Press enter to return..."; read`, cmdStr)
	c := exec.Command("sh", "-c", script)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return FlowRunDoneMsg{Name: name, Err: err}
	})
}

func Run() error {
	p := tea.NewProgram(NewApp(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

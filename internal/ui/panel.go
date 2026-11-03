package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"arlo-term/internal/arlo"
	"arlo-term/internal/relay"
	"arlo-term/internal/stream"
	"arlo-term/internal/views"
)

const (
	nameWidth   = 18
	statusWidth = 2 + len(stream.Reconnecting)
)

type Mode struct {
	Name string
	New  views.Factory
}

type (
	ReadyMsg struct {
		Cameras []arlo.Camera
		Relay   *relay.Relay
	}
	ErrorMsg  struct{ Err error }
	changeMsg struct{}
)

type Panel struct {
	program *tea.Program
	modes   []Mode
	mode    int
	cameras []arlo.Camera
	relay   *relay.Relay
	view    views.View
	spin    int
	width   int
	height  int
	Err     error
}

func NewPanel(modes []Mode) *Panel {
	return &Panel{modes: modes}
}

func (m *Panel) Attach(p *tea.Program) {
	m.program = p
}

func (m *Panel) Init() tea.Cmd {
	return tickSpinner()
}

func (m *Panel) startView() {
	if m.view != nil {
		m.view.Stop()
	}
	m.view = m.modes[m.mode].New(views.Options{
		Cameras:  m.cameras,
		Source:   m.relay,
		OnChange: m.changed,
	})
}

func (m *Panel) changed() {
	m.program.Send(changeMsg{})
}

func (m *Panel) quit() tea.Cmd {
	if m.view != nil {
		m.view.Stop()
	}
	if m.relay != nil {
		m.relay.Stop()
	}
	return tea.Quit
}

func (m *Panel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinTick:
		m.spin++
		return m, tickSpinner()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case ReadyMsg:
		m.cameras, m.relay = msg.Cameras, msg.Relay
		m.relay.Listen(m.changed)
		m.startView()
	case ErrorMsg:
		m.Err = msg.Err
		return m, m.quit()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, m.quit()
		case "tab":
			if m.view != nil {
				m.mode = (m.mode + 1) % len(m.modes)
				m.startView()
			}
		case "r":
			if m.relay != nil {
				m.relay.Reconnect()
			}
		}
	}
	return m, nil
}

func (m *Panel) View() string {
	if m.view == nil {
		return m.center(title.Render("arlo-term") + "\n\n" + spinner(m.spin) + " loading cameras")
	}
	tabs := make([]string, len(m.modes))
	for i, mode := range m.modes {
		if i == m.mode {
			tabs[i] = activeTab.Render(mode.Name)
		} else {
			tabs[i] = tab.Render(mode.Name)
		}
	}
	rows := make([]string, len(m.cameras))
	for i, cam := range m.cameras {
		status := m.view.Status(cam)
		if status == "" {
			status = stream.Connecting
		}
		rows[i] = fmt.Sprintf("%-*s%s", nameWidth, cam.Name, statusCell(status))
	}
	return m.center(strings.Join([]string{
		title.Render("arlo-term") + "  " + strings.Join(tabs, " "),
		"",
		strings.Join(rows, "\n"),
		"",
		help("tab", "mode", "r", "reload", "q", "quit"),
	}, "\n"))
}

func (m *Panel) center(content string) string {
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, page.Render(content))
}

func statusCell(s stream.Status) string {
	return lipgloss.NewStyle().Width(statusWidth).Render(statusText(s))
}

func statusText(s stream.Status) string {
	switch s {
	case stream.Live:
		return green.Render("● " + string(s))
	case stream.Connecting, stream.Reconnecting:
		return yellow.Render("◌ " + string(s))
	case stream.Offline, stream.Error:
		return red.Render("● " + string(s))
	default:
		return faint.Render("· " + string(s))
	}
}

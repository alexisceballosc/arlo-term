package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	title     = lipgloss.NewStyle().Bold(true)
	faint     = lipgloss.NewStyle().Faint(true)
	activeTab = lipgloss.NewStyle().Reverse(true).Padding(0, 1)
	tab       = faint.Padding(0, 1)
	page      = lipgloss.NewStyle().Padding(1, 2)
	accent    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	green     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellow    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	red       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	frames    = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
)

type spinTick struct{}

func tickSpinner() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinTick{} })
}

func spinner(n int) string {
	return accent.Render(frames[n%len(frames)])
}

func help(pairs ...string) string {
	parts := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, pairs[i]+" "+pairs[i+1])
	}
	return faint.Render(strings.Join(parts, "   "))
}

func Success(text string) string {
	return green.Render("✓") + " " + text
}

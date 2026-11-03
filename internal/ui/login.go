package ui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"arlo-term/internal/session"
)

const resendCooldown = 10 * time.Second

type (
	loginRequest struct {
		ctx         context.Context
		kind, label string
		secret      bool
		reply       chan string
	}
	statusMsg   string
	SignedInMsg struct{ Email string }
)

type Login struct {
	login    *loginRequest
	input    []rune
	done     bool
	resendAt time.Time
	message  string
	spin     int
	Err      error
	Email    string
	Retry    func()
}

func NewLogin() *Login {
	return &Login{message: "connecting to arlo"}
}

func LoginInteraction(p *tea.Program) session.Interaction {
	return session.Interaction{
		Status: func(message string) { p.Send(statusMsg(message)) },
		Input: func(ctx context.Context, kind, label string, secret bool) (string, error) {
			request := loginRequest{ctx: ctx, kind: kind, label: label, secret: secret, reply: make(chan string, 1)}
			p.Send(request)
			select {
			case value := <-request.reply:
				return value, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}
}

func (m *Login) Init() tea.Cmd {
	return tickSpinner()
}

func (m *Login) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinTick:
		m.spin++
		return m, tickSpinner()
	case loginRequest:
		if msg.ctx.Err() == nil {
			m.login = &msg
			m.input = nil
		}
	case statusMsg:
		m.message = string(msg)
		m.login = nil
		m.input = nil
	case SignedInMsg:
		m.Email = msg.Email
		m.done = true
		return m, tea.Quit
	case ErrorMsg:
		m.Err = msg.Err
		m.login = nil
		m.input = nil
	case tea.KeyMsg:
		switch {
		case msg.String() == "esc" || msg.String() == "ctrl+c":
			m.done = true
			return m, tea.Quit
		case m.login != nil:
			return m, m.key(msg)
		case m.Err != nil && msg.String() == "r" && m.Retry != nil:
			m.Err = nil
			m.message = "connecting to arlo"
			go m.Retry()
		}
	}
	return m, nil
}

func (m *Login) answer(value string) tea.Cmd {
	request := m.login
	select {
	case request.reply <- value:
	default:
	}
	m.input = nil
	m.login = nil
	m.message = "verifying"
	if value == "resend" || (request.kind != "email" && request.kind != "password") {
		return nil
	}
	if request.secret {
		value = strings.Repeat("•", len([]rune(value)))
	}
	return tea.Println(green.Render("✓") + " " + title.Render(request.label) + " " + accent.Render(value))
}

func (m *Login) waiting() bool {
	return m.login.kind == "push" || m.login.kind == "resend"
}

func (m *Login) key(key tea.KeyMsg) tea.Cmd {
	if m.login.ctx.Err() != nil {
		m.login = nil
		m.input = nil
		return nil
	}
	resendKey := key.String() == "ctrl+r" || (m.waiting() && key.String() == "r")
	if resendKey && m.login.kind != "email" && m.login.kind != "password" {
		if time.Now().Before(m.resendAt) {
			return nil
		}
		m.resendAt = time.Now().Add(resendCooldown)
		return m.answer("resend")
	}
	if m.waiting() {
		return nil
	}
	switch key.Type {
	case tea.KeyEnter:
		value := string(m.input)
		if !m.login.secret {
			value = strings.TrimSpace(value)
		}
		if value != "" {
			return m.answer(value)
		}
	case tea.KeyBackspace, tea.KeyCtrlH:
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case tea.KeyCtrlU:
		m.input = nil
	case tea.KeyRunes:
		for _, r := range key.Runes {
			if r >= 32 && r != 127 {
				m.input = append(m.input, r)
			}
		}
	case tea.KeySpace:
		m.input = append(m.input, ' ')
	}
	return nil
}

func (m *Login) resendHint(key string) string {
	if remaining := time.Until(m.resendAt); remaining > 0 {
		return "resend in " + remaining.Round(time.Second).String()
	}
	return key + " resend"
}

func (m *Login) View() string {
	switch {
	case m.done:
		return ""
	case m.Err != nil:
		return red.Render("✕") + " " + m.Err.Error() + "\n" + help("r", "retry", "esc", "quit") + "\n"
	case m.login == nil || m.login.ctx.Err() != nil:
		return spinner(m.spin) + " " + faint.Render(m.message) + "\n"
	}
	request := m.login
	switch request.kind {
	case "push":
		return spinner(m.spin) + " " + request.label + "\n" + faint.Render(m.resendHint("r")+"   esc cancel") + "\n"
	case "resend":
		return yellow.Render("!") + " " + request.label + "\n" + faint.Render(m.resendHint("r")+"   esc cancel") + "\n"
	}
	value := string(m.input)
	if request.secret {
		value = strings.Repeat("•", len(m.input))
	}
	line := accent.Render("?") + " " + title.Render(request.label) + " " + value + accent.Render("▏")
	if request.kind == "code" {
		return line + "\n" + faint.Render(m.resendHint("ctrl+r")+"   esc cancel") + "\n"
	}
	return line + "\n"
}

package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPasswordIsMaskedAndAcceptsQuitLetters(t *testing.T) {
	m := NewLogin()
	reply := make(chan string, 1)
	m.Update(loginRequest{ctx: context.Background(), kind: "password", label: "Password", secret: true, reply: reply})
	for _, r := range "secretqr" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if strings.Contains(m.View(), "secretqr") || !strings.Contains(m.View(), "••••••••") {
		t.Fatal("password was not masked")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if value := <-reply; value != "secretqr" {
		t.Fatalf("password corrupted: %q", value)
	}
	if len(m.input) != 0 || m.login != nil {
		t.Fatal("password retained in input state")
	}
}

func TestResendAndCooldown(t *testing.T) {
	m := NewLogin()
	reply := make(chan string, 1)
	request := loginRequest{ctx: context.Background(), kind: "push", reply: reply}
	m.Update(request)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if value := <-reply; value != "resend" {
		t.Fatal("missing resend action")
	}
	m.Update(request)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	select {
	case <-reply:
		t.Fatal("resend ignored cooldown")
	default:
	}
	m.resendAt = time.Now().Add(-time.Second)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	select {
	case <-reply:
	default:
		t.Fatal("resend stayed disabled")
	}
}

func TestCanceledLoginRequestIsIgnored(t *testing.T) {
	m := NewLogin()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.Update(loginRequest{ctx: ctx, kind: "password"})
	if m.login != nil {
		t.Fatal("stale input request shown")
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"arlo-term/internal/arlo"
	"arlo-term/internal/mpv"
	"arlo-term/internal/paths"
	"arlo-term/internal/proc"
	"arlo-term/internal/relay"
	"arlo-term/internal/session"
	"arlo-term/internal/ui"
	"arlo-term/internal/views"
)

const usage = `arlo-term: watch your Arlo cameras (alias: arlo)

usage:
  arlo-term               open the camera panel
  arlo-term login         sign in to Arlo
  arlo-term install [dir] install arlo-term and the arlo alias (default /usr/local/bin)
  arlo-term uninstall     remove arlo-term and everything it stored
`

var modes = []ui.Mode{
	{Name: "Windows", New: views.NewWindows},
	{Name: "Mosaic", New: views.NewMosaic},
}

func main() {
	paths.Migrate()
	var err error
	args := append(os.Args[1:], "")
	switch args[0] {
	case "":
		err = watch()
	case "login":
		err = login()
	case "install":
		err = install(args[1])
	case "uninstall":
		err = uninstall()
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, paths.Name+":", err)
		os.Exit(1)
	}
}

func watch() error {
	for _, bin := range []string{"mpv", "ffmpeg"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s is not installed", bin)
		}
	}
	s, err := session.Restore(context.Background())
	if errors.Is(err, session.ErrSignedOut) {
		return errors.New("not signed in, run: arlo-term login")
	}
	if err != nil {
		return err
	}
	model := ui.NewPanel(modes)
	p := tea.NewProgram(model, tea.WithAltScreen())
	model.Attach(p)
	go func() {
		client, cameras, err := arlo.Connect(s)
		if err != nil {
			p.Send(ui.ErrorMsg{Err: err})
			return
		}
		r, err := relay.Start(cameras, client.StreamURL)
		if err != nil {
			p.Send(ui.ErrorMsg{Err: err})
			return
		}
		p.Send(ui.ReadyMsg{Cameras: cameras, Relay: r})
	}()
	_, err = p.Run()
	proc.KillAll()
	mpv.Cleanup()
	if err != nil {
		return err
	}
	return model.Err
}

func login() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := ui.NewLogin()
	p := tea.NewProgram(model)
	run := func() {
		if _, email, err := session.Login(ctx, ui.LoginInteraction(p)); err != nil {
			p.Send(ui.ErrorMsg{Err: err})
		} else {
			p.Send(ui.SignedInMsg{Email: email})
		}
	}
	model.Retry = run
	go run()
	if _, err := p.Run(); err != nil {
		return err
	}
	if model.Email == "" {
		return errors.New("login canceled")
	}
	fmt.Println(ui.Success("Signed in as " + model.Email))
	return nil
}

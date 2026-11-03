package views

import (
	"fmt"
	"sync"
	"time"

	"arlo-term/internal/arlo"
	"arlo-term/internal/debug"
	"arlo-term/internal/mpv"
	"arlo-term/internal/platform"
	"arlo-term/internal/stream"
)

const (
	overlayW    = 1920
	overlayH    = 1080
	reloadDelay = 300 * time.Millisecond
	revealGap   = 200 * time.Millisecond
	revealHold  = 1500 * time.Millisecond
)

type geometry struct{ w, h, x, y int }

func windowCascade(count int, screen platform.Screen) func(int) geometry {
	step := 4 * platform.TitleBar
	spread := (count - 1) * step
	w := min(screen.Width*3/5, screen.Width-spread)
	h := int(float64(w) / aspect)
	if room := screen.Height - platform.TitleBar - spread; h > room {
		h = room
		w = int(float64(h) * aspect)
	}
	left := screen.Left + (screen.Width-w-spread)/2
	top := screen.Top + platform.TitleBar + (screen.Height-platform.TitleBar-h-spread)/2
	return func(i int) geometry {
		return geometry{w, h, left + i*step, top + i*step}
	}
}

type cameraWindow struct {
	cam      arlo.Camera
	opts     Options
	player   *mpv.Player
	playing  bool
	shown    bool
	wake     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
	unlisten func()
	mu       sync.Mutex
	status   stream.Status
}

type windowsView []*cameraWindow

func NewWindows(opts Options) View {
	place := windowCascade(len(opts.Cameras), platform.MainScreen())
	windows := make(windowsView, len(opts.Cameras))
	for i, cam := range opts.Cameras {
		windows[i] = openWindow(cam, place(i), opts)
	}
	go windows.reveal()
	return windows
}

func (v windowsView) Status(cam arlo.Camera) stream.Status {
	for _, w := range v {
		if w.cam.ID == cam.ID {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.status
		}
	}
	return ""
}

func (v windowsView) Stop() {
	for _, w := range v {
		w.close()
	}
}

func (v windowsView) reveal() {
	cleared := platform.ShowDesktop()
	for _, w := range v.alive() {
		w.player.Command("set_property", "ontop", true)
		w.player.Command("set_property", "vo", "")
		platform.Chime()
		stream.Sleep(revealGap, w.stop)
	}
	if cleared {
		time.Sleep(revealHold)
	}
	for _, w := range v.alive() {
		w.player.Command("set_property", "ontop", false)
		platform.Activate(w.player.Pid())
	}
}

func (v windowsView) alive() windowsView {
	var alive windowsView
	for _, w := range v {
		select {
		case <-w.stop:
		default:
			alive = append(alive, w)
		}
	}
	return alive
}

func openWindow(cam arlo.Camera, g geometry, opts Options) *cameraWindow {
	w := &cameraWindow{cam: cam, opts: opts, wake: make(chan struct{}, 1), stop: make(chan struct{})}
	player, err := mpv.Start(append([]string{
		"--idle=yes", "--keep-open=always", "--force-window=yes", "--vo=null",
		"--title=" + cam.Name, fmt.Sprintf("--geometry=%dx%d+%d+%d", g.w, g.h, g.x, g.y),
	}, platform.WindowOptions...), false)
	if err != nil {
		debug.Log(cam.Name, err.Error())
		w.setStatus(stream.Error)
		w.close()
		return w
	}
	w.player = player
	w.unlisten = opts.Source.Listen(func() { poke(w.wake) })
	go func() {
		<-player.Exited
		select {
		case <-w.stop:
		default:
			w.close()
			w.setStatus(stream.Closed)
		}
	}()
	go w.loop()
	return w
}

func (w *cameraWindow) setStatus(s stream.Status) {
	w.mu.Lock()
	changed := w.status != s
	w.status = s
	w.mu.Unlock()
	if !changed {
		return
	}
	if w.player != nil && s != stream.Closed {
		text := ""
		if s != stream.Live {
			text = mpv.Label(string(s), overlayW/2, overlayH/2, 44, mpv.Center)
		}
		w.player.Overlay(text, overlayW, overlayH)
	}
	w.opts.OnChange()
}

func (w *cameraWindow) refresh() stream.Status {
	source := w.opts.Source.Status(w.cam)
	select {
	case <-w.stop:
		return source
	default:
	}
	if source == stream.Offline && w.shown {
		w.shown = false
		w.player.Command("stop")
	}
	w.setStatus(display(source, w.playing, w.shown))
	return source
}

func (w *cameraWindow) loop() {
	w.player.Command("observe_property", 1, "eof-reached")
	for {
		for w.refresh() != stream.Live {
			select {
			case <-w.wake:
			case <-w.stop:
				return
			}
		}
		if !w.play(w.opts.Source.URL(w.cam)) || !stream.Sleep(reloadDelay, w.stop) {
			return
		}
	}
}

func (w *cameraWindow) play(url string) bool {
	defer func() {
		w.playing = false
		w.refresh()
	}()
	w.player.Command("loadfile", url)
	timeout := time.NewTimer(stream.ConnectTimeout)
	defer timeout.Stop()
	for {
		select {
		case ev := <-w.player.Events:
			switch {
			case ev.Event == "playback-restart":
				w.playing, w.shown = true, true
				w.refresh()
			case ev.Event == "end-file" && ev.Reason != "stop":
				return true
			case ev.Event == "property-change" && ev.Name == "eof-reached" && string(ev.Data) == "true":
				return true
			}
		case <-w.wake:
			w.refresh()
		case <-timeout.C:
			if !w.playing {
				w.player.Command("stop")
				return true
			}
		case <-w.stop:
			return false
		}
	}
}

func (w *cameraWindow) close() {
	w.stopOnce.Do(func() {
		close(w.stop)
		if w.unlisten != nil {
			w.unlisten()
		}
		if w.player != nil {
			w.player.Quit()
		}
	})
}

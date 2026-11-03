package views

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"arlo-term/internal/arlo"
	"arlo-term/internal/mpv"
	"arlo-term/internal/platform"
	"arlo-term/internal/stream"
)

const (
	mosaicGap = 4
	mosaicFPS = 15
	dimFactor = 0.35
	canvases  = 3
)

type layout struct {
	tileW, tileH, width, height, cols int
}

func (l layout) at(i int) (int, int) {
	return i % l.cols * (l.tileW + mosaicGap), i / l.cols * (l.tileH + mosaicGap)
}

func mosaicLayout(count int, screen platform.Screen) layout {
	cols, w, h := fitGrid(count, screen.Width, screen.Height-platform.TitleBar, mosaicGap, mosaicGap)
	w, h = even(w), even(h)
	rows := (count + cols - 1) / cols
	return layout{w, h, cols*w + (cols-1)*mosaicGap, rows*h + (rows-1)*mosaicGap, cols}
}

type mosaic struct {
	layout
	player   *mpv.Player
	feeds    []*feed
	labels   chan struct{}
	free     chan []byte
	frames   chan []byte
	stop     chan struct{}
	stopOnce sync.Once
	closed   atomic.Bool
	unlisten func()
}

func NewMosaic(opts Options) View {
	m := &mosaic{
		layout: mosaicLayout(len(opts.Cameras), platform.MainScreen()),
		labels: make(chan struct{}, 1),
		free:   make(chan []byte, canvases),
		frames: make(chan []byte, 1),
		stop:   make(chan struct{}),
	}
	for range canvases {
		m.free <- make([]byte, m.width*m.height*3)
	}
	notify := func() {
		poke(m.labels)
		opts.OnChange()
	}
	m.unlisten = opts.Source.Listen(notify)

	player, err := mpv.Start([]string{
		"--demuxer=rawvideo",
		fmt.Sprintf("--demuxer-rawvideo-w=%d", m.width),
		fmt.Sprintf("--demuxer-rawvideo-h=%d", m.height),
		"--demuxer-rawvideo-mp-format=rgb24",
		fmt.Sprintf("--demuxer-rawvideo-fps=%d", mosaicFPS),
		"--untimed", "--title=Arlo",
		fmt.Sprintf("--geometry=%dx%d", m.width, m.height),
		"-",
	}, true)
	if err != nil {
		m.closed.Store(true)
		return m
	}
	m.player = player
	for _, cam := range opts.Cameras {
		m.feeds = append(m.feeds, startFeed(cam, opts.Source, m.tileW, m.tileH, mosaicFPS, notify))
	}
	go m.write()
	go m.compose()
	go m.drawLabels()
	go func() {
		<-player.Exited
		select {
		case <-m.stop:
		default:
			m.closed.Store(true)
			m.Stop()
			opts.OnChange()
		}
	}()
	return m
}

func (m *mosaic) Status(cam arlo.Camera) stream.Status {
	if m.closed.Load() {
		return stream.Closed
	}
	for _, f := range m.feeds {
		if f.cam.ID == cam.ID {
			return f.status()
		}
	}
	return ""
}

func (m *mosaic) Stop() {
	m.stopOnce.Do(func() {
		close(m.stop)
		m.unlisten()
		for _, f := range m.feeds {
			f.close()
		}
		if m.player != nil {
			m.player.Quit()
		}
	})
}

func (m *mosaic) compose() {
	ticker := time.NewTicker(time.Second / mosaicFPS)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-m.stop:
			return
		}
		var canvas []byte
		select {
		case canvas = <-m.free:
		default:
			continue
		}
		for i, f := range m.feeds {
			x, y := m.at(i)
			m.drawTile(canvas, f.latest(), x, y, f.status() != stream.Live)
		}
		select {
		case m.frames <- canvas:
		case <-m.stop:
			return
		}
	}
}

func (m *mosaic) drawTile(canvas, frame []byte, x, y int, dimmed bool) {
	row := m.tileW * 3
	for r := range m.tileH {
		dst := canvas[((y+r)*m.width+x)*3:][:row]
		switch {
		case frame == nil:
			for i := range dst {
				dst[i] = 12
			}
		case dimmed:
			for i, v := range frame[r*row : (r+1)*row] {
				dst[i] = byte(float64(v) * dimFactor)
			}
		default:
			copy(dst, frame[r*row:(r+1)*row])
		}
	}
}

func (m *mosaic) write() {
	for {
		select {
		case canvas := <-m.frames:
			_, err := m.player.Stdin.Write(canvas)
			m.free <- canvas
			if err != nil {
				return
			}
		case <-m.stop:
			return
		}
	}
}

func (m *mosaic) drawLabels() {
	nameSize := max(13, m.tileH/26)
	statusSize := max(16, m.tileH/16)
	for {
		var events []string
		for i, f := range m.feeds {
			x, y := m.at(i)
			events = append(events, mpv.Label(f.cam.Name, x+8, y+8, nameSize, mpv.TopLeft))
			if s := f.status(); s != stream.Live {
				events = append(events, mpv.Label(string(s), x+m.tileW/2, y+m.tileH/2, statusSize, mpv.Center))
			}
		}
		m.player.Overlay(strings.Join(events, "\n"), m.width, m.height)
		select {
		case <-m.labels:
		case <-m.stop:
			return
		}
	}
}

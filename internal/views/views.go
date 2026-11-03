package views

import (
	"arlo-term/internal/arlo"
	"arlo-term/internal/stream"
)

const aspect = 16.0 / 9.0

type View interface {
	Status(arlo.Camera) stream.Status
	Stop()
}

type Source interface {
	URL(arlo.Camera) string
	Status(arlo.Camera) stream.Status
	Listen(func()) func()
}

type Options struct {
	Cameras  []arlo.Camera
	Source   Source
	OnChange func()
}

type Factory func(Options) View

func display(source stream.Status, playing, shown bool) stream.Status {
	switch {
	case source == "":
		return stream.Connecting
	case source != stream.Live:
		return source
	case playing:
		return stream.Live
	case shown:
		return stream.Reconnecting
	default:
		return stream.Connecting
	}
}

func fitGrid(count, width, height, gapX, gapY int) (cols, w, h int) {
	cols = 1
	for cols*cols < count {
		cols++
	}
	rows := (count + cols - 1) / cols
	w = (width - (cols-1)*gapX) / cols
	h = int(float64(w) / aspect)
	if rows*h+(rows-1)*gapY > height {
		h = (height - (rows-1)*gapY) / rows
		w = int(float64(h) * aspect)
	}
	return cols, w, h
}

func poke(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func even(n int) int {
	return max(2, n/2*2)
}

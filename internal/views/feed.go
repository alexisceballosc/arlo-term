package views

import (
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"

	"arlo-term/internal/arlo"
	"arlo-term/internal/proc"
	"arlo-term/internal/stream"
)

const frameRing = 3

type feed struct {
	cam      arlo.Camera
	source   Source
	notify   func()
	width    int
	height   int
	fps      int
	frameMu  sync.Mutex
	frame    []byte
	fresh    atomic.Bool
	cmd      atomic.Pointer[exec.Cmd]
	stop     chan struct{}
	stopOnce sync.Once
}

func startFeed(cam arlo.Camera, source Source, width, height, fps int, notify func()) *feed {
	f := &feed{cam: cam, source: source, notify: notify, width: width, height: height, fps: fps, stop: make(chan struct{})}
	go f.loop()
	return f
}

func (f *feed) latest() []byte {
	f.frameMu.Lock()
	defer f.frameMu.Unlock()
	return f.frame
}

func (f *feed) status() stream.Status {
	return display(f.source.Status(f.cam), f.fresh.Load(), f.latest() != nil)
}

func (f *feed) loop() {
	for {
		f.decode(f.source.URL(f.cam))
		if f.fresh.Swap(false) {
			f.notify()
		}
		if !stream.Sleep(reloadDelay, f.stop) {
			return
		}
	}
}

func (f *feed) decode(url string) {
	cmd := exec.Command("ffmpeg", "-loglevel", "error", "-i", url, "-an",
		"-vf", fmt.Sprintf("fps=%d,scale=%d:%d", f.fps, f.width, f.height), "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1")
	stdout, err := cmd.StdoutPipe()
	if err != nil || proc.Start(cmd) != nil {
		return
	}
	f.cmd.Store(cmd)
	size := f.width * f.height * 3
	ring := [frameRing][]byte{}
	for i := range ring {
		ring[i] = make([]byte, size)
	}
	for n := 0; ; n++ {
		buf := ring[n%frameRing]
		if _, err := io.ReadFull(stdout, buf); err != nil {
			break
		}
		f.frameMu.Lock()
		f.frame = buf
		f.frameMu.Unlock()
		if !f.fresh.Swap(true) {
			f.notify()
		}
	}
	proc.Wait(cmd)
}

func (f *feed) close() {
	f.stopOnce.Do(func() {
		close(f.stop)
		if cmd := f.cmd.Load(); cmd != nil {
			proc.Kill(cmd)
		}
	})
}

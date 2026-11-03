package relay

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"arlo-term/internal/arlo"
	"arlo-term/internal/debug"
	"arlo-term/internal/proc"
	"arlo-term/internal/stream"
)

const (
	chunkPackets     = 64
	subscriberBuffer = 512
)

type Relay struct {
	base      string
	server    *http.Server
	sources   map[string]*source
	listenMu  sync.Mutex
	listeners map[int]func()
	nextID    int
}

func Start(cameras []arlo.Camera, streamURL stream.URLFunc) (*Relay, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	r := &Relay{base: "http://" + ln.Addr().String() + "/", sources: map[string]*source{}, listeners: map[int]func(){}}
	for _, cam := range cameras {
		r.sources[cam.ID] = newSource(cam, streamURL, r.notify)
	}
	r.server = &http.Server{Handler: http.HandlerFunc(r.serve)}
	go r.server.Serve(ln)
	for _, s := range r.sources {
		go s.loop()
	}
	return r, nil
}

func (r *Relay) URL(cam arlo.Camera) string {
	return r.base + cam.ID
}

func (r *Relay) Status(cam arlo.Camera) stream.Status {
	if s := r.sources[cam.ID]; s != nil {
		return s.get()
	}
	return ""
}

func (r *Relay) Received(cam arlo.Camera) int64 {
	if s := r.sources[cam.ID]; s != nil {
		return s.received.Load()
	}
	return 0
}

func (r *Relay) Listen(fn func()) func() {
	r.listenMu.Lock()
	defer r.listenMu.Unlock()
	r.nextID++
	id := r.nextID
	r.listeners[id] = fn
	return func() {
		r.listenMu.Lock()
		delete(r.listeners, id)
		r.listenMu.Unlock()
	}
}

func (r *Relay) notify() {
	r.listenMu.Lock()
	fns := make([]func(), 0, len(r.listeners))
	for _, fn := range r.listeners {
		fns = append(fns, fn)
	}
	r.listenMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (r *Relay) Reconnect() {
	for _, s := range r.sources {
		s.restart()
	}
}

func (r *Relay) Stop() {
	r.server.Close()
	for _, s := range r.sources {
		s.close()
	}
}

func (r *Relay) serve(w http.ResponseWriter, req *http.Request) {
	s := r.sources[strings.TrimPrefix(req.URL.Path, "/")]
	if s == nil {
		http.NotFound(w, req)
		return
	}
	ch, cancel := s.subscribe()
	defer cancel()
	w.Header().Set("Content-Type", "video/mp2t")
	flusher, _ := w.(http.Flusher)
	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-req.Context().Done():
			return
		}
	}
}

type source struct {
	cam       arlo.Camera
	streamURL stream.URLFunc
	notify    func()
	retry     stream.RetryPolicy
	statusMu  sync.Mutex
	status    stream.Status
	subMu     sync.Mutex
	subs      map[chan []byte]struct{}
	cache     *gopCache
	cmd       atomic.Pointer[exec.Cmd]
	received  atomic.Int64
	stop      chan struct{}
	stopOnce  sync.Once
}

func newSource(cam arlo.Camera, streamURL stream.URLFunc, notify func()) *source {
	return &source{cam: cam, streamURL: streamURL, notify: notify, subs: map[chan []byte]struct{}{}, cache: newGOPCache(), stop: make(chan struct{})}
}

func (s *source) get() stream.Status {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	return s.status
}

func (s *source) setStatus(status stream.Status) {
	s.statusMu.Lock()
	changed := s.status != status
	s.status = status
	s.statusMu.Unlock()
	if changed {
		debug.Log(s.cam.Name, "status "+string(status))
		s.notify()
	}
}

func (s *source) subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, subscriberBuffer)
	s.subMu.Lock()
	if gop := s.cache.snapshot(); gop != nil {
		ch <- gop
	}
	s.subs[ch] = struct{}{}
	s.subMu.Unlock()
	return ch, func() {
		s.subMu.Lock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
		s.subMu.Unlock()
	}
}

func (s *source) publish(chunk []byte) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for off := 0; off+packetSize <= len(chunk); off += packetSize {
		s.cache.add(packet(chunk[off : off+packetSize]))
	}
	for ch := range s.subs {
		select {
		case ch <- chunk:
		default:
			delete(s.subs, ch)
			close(ch)
		}
	}
}

func (s *source) reset() {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for ch := range s.subs {
		close(ch)
	}
	s.subs = map[chan []byte]struct{}{}
	s.cache = newGOPCache()
}

func (s *source) loop() {
	for {
		if s.get() == "" {
			s.setStatus(stream.Connecting)
		}
		url, err := s.streamURL(s.cam)
		if err != nil {
			debug.Log(s.cam.Name, err.Error())
			s.setStatus(stream.Error)
			if !stream.Sleep(stream.ErrorRetry, s.stop) {
				return
			}
			continue
		}
		received, log := s.ingest(url)
		s.reset()
		select {
		case <-s.stop:
			return
		default:
		}
		status, wait := s.retry.Next(log, !received)
		s.setStatus(status)
		if !stream.Sleep(wait, s.stop) {
			return
		}
	}
}

func (s *source) ingest(url string) (bool, string) {
	args := slices.Concat([]string{"-loglevel", "warning"}, stream.FFmpegInput, []string{
		"-i", url, "-map", "0:v:0", "-c", "copy", "-f", "mpegts", "pipe:1",
	})
	cmd := exec.Command("ffmpeg", args...)
	var log bytes.Buffer
	cmd.Stderr = &logWriter{name: s.cam.Name, buf: &log}
	stdout, err := cmd.StdoutPipe()
	if err != nil || proc.Start(cmd) != nil {
		return false, ""
	}
	s.cmd.Store(cmd)
	var received atomic.Bool
	watchdog := time.AfterFunc(stream.ConnectTimeout, func() {
		if !received.Load() {
			proc.Kill(cmd)
		}
	})
	defer watchdog.Stop()

	reader := bufio.NewReaderSize(stdout, packetSize*chunkPackets*4)
	for {
		chunk, err := readChunk(reader)
		if len(chunk) > 0 {
			if !received.Swap(true) {
				s.setStatus(stream.Live)
			}
			s.received.Add(int64(len(chunk)))
			s.publish(chunk)
		}
		if err != nil {
			break
		}
	}
	proc.Wait(cmd)
	return received.Load(), log.String()
}

func readChunk(r *bufio.Reader) ([]byte, error) {
	chunk := make([]byte, 0, packetSize*chunkPackets)
	for len(chunk) < cap(chunk) {
		p := chunk[len(chunk) : len(chunk)+packetSize]
		if _, err := io.ReadFull(r, p); err != nil {
			return chunk, err
		}
		if p[0] != syncByte {
			return chunk, io.ErrUnexpectedEOF
		}
		chunk = chunk[:len(chunk)+packetSize]
		if r.Buffered() < packetSize {
			break
		}
	}
	return chunk, nil
}

func (s *source) restart() {
	if cmd := s.cmd.Load(); cmd != nil {
		proc.Kill(cmd)
	}
}

func (s *source) close() {
	s.stopOnce.Do(func() {
		close(s.stop)
		s.restart()
		s.reset()
	})
}

type logWriter struct {
	name string
	buf  *bytes.Buffer
}

func (w *logWriter) Write(p []byte) (int, error) {
	debug.Log(w.name, string(p))
	if w.buf.Len() < 8000 {
		w.buf.Write(p)
	}
	return len(p), nil
}

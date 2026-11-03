package mpv

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"arlo-term/internal/debug"
	"arlo-term/internal/proc"
)

const font = "Menlo"

const SocketPrefix = "arlo-term-mpv-"

var counter atomic.Int64

var baseOptions = []string{
	"--profile=low-latency",
	"--no-audio",
	"--osc=no",
	"--no-config",
	"--msg-level=all=warn",
	"--osd-font=" + font,
}

type Event struct {
	Event  string          `json:"event"`
	Reason string          `json:"reason"`
	Name   string          `json:"name"`
	Data   json.RawMessage `json:"data"`
}

type Player struct {
	Events chan Event
	Exited chan struct{}
	Stdin  io.WriteCloser

	cmd     *exec.Cmd
	conn    net.Conn
	ready   chan struct{}
	mu      sync.Mutex
	nextID  int
	pending map[int]chan struct{}
}

func Start(args []string, withStdin bool) (*Player, error) {
	socket := filepath.Join(os.TempDir(), fmt.Sprintf("%s%d-%d.sock", SocketPrefix, os.Getpid(), counter.Add(1)))
	cmd := exec.Command("mpv", slices.Concat(baseOptions, []string{"--input-ipc-server=" + socket}, args)...)
	p := &Player{
		Events:  make(chan Event, 64),
		Exited:  make(chan struct{}),
		cmd:     cmd,
		ready:   make(chan struct{}),
		pending: map[int]chan struct{}{},
	}
	if withStdin {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		p.Stdin = stdin
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := proc.Start(cmd); err != nil {
		return nil, err
	}
	go p.readLog(stdout)
	go func() {
		proc.Wait(cmd)
		os.Remove(socket)
		close(p.Exited)
	}()
	go p.connect(socket)
	return p, nil
}

func (p *Player) readLog(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		debug.Log("mpv", scanner.Text())
	}
}

func (p *Player) connect(socket string) {
	defer close(p.ready)
	for range 50 {
		select {
		case <-p.Exited:
			return
		default:
		}
		conn, err := net.Dial("unix", socket)
		if err == nil {
			p.conn = conn
			go p.readEvents()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (p *Player) readEvents() {
	scanner := bufio.NewScanner(p.conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var msg struct {
			Event
			RequestID int `json:"request_id"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}
		if msg.Event.Event != "" {
			select {
			case p.Events <- msg.Event:
			default:
			}
			continue
		}
		p.mu.Lock()
		ch := p.pending[msg.RequestID]
		delete(p.pending, msg.RequestID)
		p.mu.Unlock()
		if ch != nil {
			close(ch)
		}
	}
}

func (p *Player) send(command any) {
	<-p.ready
	if p.conn == nil {
		return
	}
	p.mu.Lock()
	p.nextID++
	id := p.nextID
	done := make(chan struct{})
	p.pending[id] = done
	b, _ := json.Marshal(map[string]any{"command": command, "request_id": id})
	_, err := p.conn.Write(append(b, '\n'))
	p.mu.Unlock()
	if err != nil {
		return
	}
	select {
	case <-done:
	case <-p.Exited:
	case <-time.After(5 * time.Second):
	}
}

func (p *Player) Command(args ...any) {
	p.send(args)
}

func (p *Player) Overlay(events string, resX, resY int) {
	p.send(map[string]any{"name": "osd-overlay", "id": 1, "format": "ass-events", "data": events, "res_x": resX, "res_y": resY})
}

func Cleanup() {
	sockets, _ := filepath.Glob(filepath.Join(os.TempDir(), fmt.Sprintf("%s%d-*.sock", SocketPrefix, os.Getpid())))
	for _, socket := range sockets {
		os.Remove(socket)
	}
}

func (p *Player) Pid() int {
	return p.cmd.Process.Pid
}

func (p *Player) Quit() {
	proc.Kill(p.cmd)
}

type Align int

const (
	TopLeft Align = 7
	Center  Align = 5
)

func Label(text string, x, y, size int, align Align) string {
	w := int(math.Ceil(float64(len(text))*float64(size)*0.6 + float64(size)))
	h := int(math.Ceil(float64(size) * 1.5))
	bx, by := x, y
	if align == Center {
		bx, by = x-w/2, y-h/2
	}
	box := fmt.Sprintf(`{\an7\pos(%d,%d)\bord0\shad0\1c&H000000&\1a&H50&\p1}m 0 0 l %d 0 %d %d 0 %d{\p0}`, bx, by, w, w, h, h)
	label := fmt.Sprintf(`{\an5\pos(%d,%d)\fn%s\fs%d\bord0\shad0\1c&HFFFFFF&}%s`, bx+w/2, by+h/2, font, size, escape(text))
	return box + "\n" + label
}

func escape(text string) string {
	return strings.NewReplacer(`\`, `\\`, "{", `\{`, "}", `\}`).Replace(text)
}

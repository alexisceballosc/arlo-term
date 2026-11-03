package proc

import (
	"os/exec"
	"sync"
)

var (
	mu       sync.Mutex
	children = map[*exec.Cmd]struct{}{}
)

func Start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	mu.Lock()
	children[cmd] = struct{}{}
	mu.Unlock()
	return nil
}

func Wait(cmd *exec.Cmd) error {
	err := cmd.Wait()
	mu.Lock()
	delete(children, cmd)
	mu.Unlock()
	return err
}

func Kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}

func KillAll() {
	mu.Lock()
	defer mu.Unlock()
	for cmd := range children {
		Kill(cmd)
	}
}

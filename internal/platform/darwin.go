//go:build darwin

package platform

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"time"

	"arlo-term/internal/proc"
)

const TitleBar = 28

const desktopAnimation = 700 * time.Millisecond

var WindowOptions = []string{"--focus-on=never"}

const screenScript = `ObjC.import("AppKit"); const s = $.NSScreen.mainScreen; const f = s.visibleFrame;
JSON.stringify({ x: f.origin.x, y: f.origin.y, w: f.size.width, h: f.size.height, full: s.frame.size.height })`

func MainScreen() Screen {
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", screenScript).Output()
	var f struct{ X, Y, W, H, Full float64 }
	if err != nil || json.Unmarshal(out, &f) != nil {
		return Screen{0, 25, 1440, 875}
	}
	return Screen{int(f.X), int(f.Full - f.Y - f.H), int(f.W), int(f.H)}
}

func Chime() {
	cmd := exec.Command("afplay", "/System/Library/Sounds/Sosumi.aiff")
	if proc.Start(cmd) == nil {
		go proc.Wait(cmd)
	}
}

func ShowDesktop() bool {
	if exec.Command("osascript", "-e", `tell application "System Events" to key code 103`).Run() != nil {
		return false
	}
	time.Sleep(desktopAnimation)
	return true
}

func Activate(pid int) {
	exec.Command("osascript", "-e", `tell application "System Events" to set frontmost of (first application process whose unix id is `+strconv.Itoa(pid)+`) to true`).Run()
}

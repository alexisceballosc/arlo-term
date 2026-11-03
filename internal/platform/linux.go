//go:build linux

package platform

import (
	"os/exec"
	"regexp"
	"strconv"

	"arlo-term/internal/proc"
)

const TitleBar = 32

var WindowOptions []string

var primaryMode = regexp.MustCompile(`primary (\d+)x(\d+)\+(\d+)\+(\d+)`)

func MainScreen() Screen {
	out, _ := exec.Command("xrandr", "--current").Output()
	m := primaryMode.FindStringSubmatch(string(out))
	if m == nil {
		return Screen{0, 0, 1920, 1080}
	}
	n := func(s string) int { v, _ := strconv.Atoi(s); return v }
	return Screen{n(m[3]), n(m[4]), n(m[1]), n(m[2])}
}

func Chime() {
	cmd := exec.Command("paplay", "/usr/share/sounds/freedesktop/stereo/bell.oga")
	if proc.Start(cmd) == nil {
		go proc.Wait(cmd)
	}
}

func ShowDesktop() bool { return false }

func Activate(int) {}

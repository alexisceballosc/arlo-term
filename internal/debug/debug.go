package debug

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"arlo-term/internal/paths"
)

var (
	mu     sync.Mutex
	path   = logPath()
	secret = regexp.MustCompile(`(egressToken|txnId)=[^&\s]+`)
)

func logPath() string {
	if os.Getenv("ARLO_DEBUG") == "" {
		return ""
	}
	p, err := paths.File("debug.log")
	if err != nil || os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return ""
	}
	return p
}

func Log(source, text string) {
	if path == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	for _, line := range strings.Split(secret.ReplaceAllString(text, "$1=***"), "\n") {
		if strings.TrimSpace(line) != "" {
			fmt.Fprintf(f, "[%s] %s\n", source, line)
		}
	}
}

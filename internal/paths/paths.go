package paths

import (
	"os"
	"path/filepath"
)

const (
	Name   = "arlo-term"
	Alias  = "arlo"
	legacy = "arlo"
)

func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, Name), nil
}

func File(file string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, file), nil
}

func Migrate() {
	base, err := os.UserConfigDir()
	if err != nil {
		return
	}
	old := filepath.Join(base, legacy)
	for _, file := range []string{"auth.json", "debug.log"} {
		from := filepath.Join(old, file)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		to, err := File(file)
		if err != nil || os.MkdirAll(filepath.Dir(to), 0o700) != nil {
			return
		}
		if _, err := os.Stat(to); os.IsNotExist(err) {
			os.Rename(from, to)
		}
	}
	os.Remove(old)
}

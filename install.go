package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"arlo-term/internal/mpv"
	"arlo-term/internal/paths"
	"arlo-term/internal/ui"
)

func executable() (string, error) {
	bin, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	return bin, nil
}

func install(dir string) error {
	if dir == "" {
		dir = "/usr/local/bin"
	}
	src, err := executable()
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, paths.Name)
	alias := filepath.Join(dir, paths.Alias)
	if src != dst {
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		tmp := dst + ".tmp"
		if err := os.WriteFile(tmp, b, 0o755); err != nil {
			return fmt.Errorf("%w (try: sudo %s install %s)", err, src, dir)
		}
		if err := os.Rename(tmp, dst); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if target, err := os.Readlink(alias); err == nil && target == paths.Name {
		fmt.Println(ui.Success("Installed " + dst + " and " + alias))
		return nil
	}
	if _, err := os.Lstat(alias); err == nil {
		fmt.Println(ui.Success("Installed " + dst + " (" + alias + " already exists, alias skipped)"))
		return nil
	}
	if err := os.Symlink(paths.Name, alias); err != nil {
		return err
	}
	fmt.Println(ui.Success("Installed " + dst + " and " + alias))
	return nil
}

func uninstall() error {
	dir, err := paths.Dir()
	if err != nil {
		return err
	}
	bin, err := executable()
	if err != nil {
		return err
	}
	targets := []string{bin}
	alias := filepath.Join(filepath.Dir(bin), paths.Alias)
	if target, err := os.Readlink(alias); err == nil && target == paths.Name {
		targets = append(targets, alias)
	}
	targets = append(targets, dir)
	fmt.Printf("This removes:\n  %s\nContinue? [y/N] ", strings.Join(targets, "\n  "))
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if !strings.EqualFold(strings.TrimSpace(answer), "y") {
		fmt.Println("Nothing removed.")
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	sockets, _ := filepath.Glob(filepath.Join(os.TempDir(), mpv.SocketPrefix+"*.sock"))
	for _, socket := range sockets {
		os.Remove(socket)
	}
	for _, path := range targets[:len(targets)-1] {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("data removed, but %s could not be: %w (try: sudo rm %s)", path, err, path)
		}
	}
	fmt.Println(ui.Success(paths.Name + " removed"))
	return nil
}

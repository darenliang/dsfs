package main

import (
	"path/filepath"
	"strings"
)

func getDir(path string) string {
	// Handle weirdness with Windows
	return strings.ReplaceAll(filepath.Dir(path), "\\", "/")
}

package main

import (
	jsoniter "github.com/json-iterator/go"
	"path/filepath"
	"strings"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

func getDir(path string) string {
	// Handle weirdness with Windows
	return strings.ReplaceAll(filepath.Dir(path), "\\", "/")
}

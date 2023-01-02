package main

import (
	jsoniter "github.com/json-iterator/go"
	"path/filepath"
	"strings"
)

// json uses json-iterator's json serializer for better performance
var json = jsoniter.ConfigFastest

func getDir(path string) string {
	// Handle weirdness with Windows
	return strings.ReplaceAll(filepath.Dir(path), "\\", "/")
}

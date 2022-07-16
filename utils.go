package main

import (
	"path/filepath"
	"strings"
)

func getDir(path string) string {
	// Handle weirdness with Windows
	return strings.ReplaceAll(filepath.Dir(path), "\\", "/")
}

type Range struct {
	start, end int64
}

type Load struct {
	ranges []Range
}

func (load *Load) addRange(start, end int64) {
	load.ranges = append(load.ranges, Range{start, end})
}

func (load *Load) truncate(end int64) {
	var newRanges []Range
	for _, v := range load.ranges {
		if v.start >= end {
			continue
		}
		if v.end > end {
			newRanges = append(newRanges, Range{v.start, end})
			continue
		}
		newRanges = append(newRanges, v)
	}
	load.ranges = newRanges
}

func (load *Load) isReady(start, end int64) bool {
	for i := start; i < end; i++ {
		seen := false
		for _, v := range load.ranges {
			if v.start <= i && i < v.end {
				seen = true
				i = v.end - 1
				break
			}
		}
		if !seen {
			return false
		}
	}
	return true
}

func newLoad() *Load {
	return &Load{ranges: make([]Range, 0)}
}

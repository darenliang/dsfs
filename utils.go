package main

import (
	"golang.org/x/exp/slices"
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

// sortRanges sorts ranges by start index
// This uses generics for faster sorting performance so go 1.18+ is required
func (load *Load) sortRanges() {
	slices.SortFunc(load.ranges, func(a, b Range) bool {
		return a.start < b.start
	})
}

// addRange adds a new range
func (load *Load) addRange(start, end int64) {
	load.ranges = append(load.ranges, Range{start, end})
}

// removeRange removes a range at index
func (load *Load) removeRange(i int) {
	last := len(load.ranges) - 1
	load.ranges[i] = load.ranges[last]
	load.ranges = load.ranges[:last]
}

// truncate trims all ranges that are over end
// Does not preserve range orders at the benefit of speed
func (load *Load) truncate(end int64) {
	for i, v := range load.ranges {
		if v.start >= end {
			load.removeRange(i)
		} else if v.end > end {
			load.ranges[i].end = end
		}
	}
}

// isReady determines if range [start, end) is covered
// This function is optimized to satisfy O(n log n) time with O(1) space
func (load *Load) isReady(start, end int64) bool {
	load.sortRanges()

	for _, v := range load.ranges {
		if v.start > start {
			return false
		} else if v.end > start {
			if v.end < end {
				start = v.end
			} else {
				return true
			}
		}
	}

	return false
}

// bytesReady determines the maximum number of bytes ready given start
// This function is optimized to satisfy O(n log n) time with O(1) space
func (load *Load) bytesReady(start int64) int64 {
	load.sortRanges()

	ptr := start
	for _, v := range load.ranges {
		if v.start > ptr {
			break
		}
		if v.end >= ptr {
			ptr = v.end
		}
	}

	return ptr - start
}

func newLoad() *Load {
	return &Load{ranges: make([]Range, 0)}
}

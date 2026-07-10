package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

type fileEntry struct {
	Path string
	Size int64
}

func walkdir(root string) ([]fileEntry, error) {
	var entries []fileEntry

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Log permission errors and continue
			if os.IsPermission(err) || strings.Contains(err.Error(), "Access is denied") {
				fmt.Fprintf(os.Stderr, "skipping %s: %v\n", path, err)
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if os.IsPermission(err) || strings.Contains(err.Error(), "Access is denied") {
				fmt.Fprintf(os.Stderr, "skipping %s: %v\n", path, err)
				return nil
			}
			return err
		}
		alloc := getAllocatedSize(path, info)
		entries = append(entries, fileEntry{Path: path, Size: alloc})
		return nil
	})

	return entries, err
}

func aggregateByDir(entries []fileEntry) map[string]int64 {
	totals := make(map[string]int64)
	for _, e := range entries {
		dir := filepath.Dir(e.Path)
		for {
			totals[dir] += e.Size
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return totals
}

var (
	modkernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetCompressedFileSizeW = modkernel32.NewProc("GetCompressedFileSizeW")
)

func getAllocatedSize(path string, info os.FileInfo) int64 {
	if runtime.GOOS != "windows" {
		return info.Size()
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return info.Size()
	}
	var high uint32
	r1, _, e := procGetCompressedFileSizeW.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&high)))
	low := uint32(r1)
	if r1 == 0xFFFFFFFF && e != nil && e != syscall.Errno(0) {
		return info.Size()
	}
	size := (uint64(high) << 32) | uint64(low)
	return int64(size)
}

func humanSize(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func main() {
	root := "C:\\"
	absRoot, _ := filepath.Abs(root)

	entries, err := walkdir(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error scanning:", err)
		os.Exit(1)
	}

	var totalFiles int
	var totalBytes int64
	for _, e := range entries {
		totalFiles++
		totalBytes += e.Size
	}

	totals := aggregateByDir(entries)

	type dirTotal struct {
		Path string
		Size int64
	}

	var list []dirTotal
	for p, s := range totals {
		rel, err := filepath.Rel(absRoot, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		list = append(list, dirTotal{Path: p, Size: s})
	}

	sort.Slice(list, func(i, j int) bool { return list[i].Size > list[j].Size })

	top := 20
	if len(list) < top {
		top = len(list)
	}

	fmt.Printf("Top %d directories under %s:\n", top, absRoot)
	for i := 0; i < top; i++ {
		fmt.Printf("%10s  %s\n", humanSize(list[i].Size), list[i].Path)
	}

	fmt.Printf("\nFiles: %d\nTotal size: %s\n", totalFiles, humanSize(totalBytes))
}

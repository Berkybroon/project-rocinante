package main

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

const maxFilesPerFolder = 10

var (
	modkernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetCompressedFileSizeW = modkernel32.NewProc("GetCompressedFileSizeW")
)

// treeNode represents a node in the directory tree. can be either a file or a directory
type treeNode struct {
	Name        string
	Path        string
	Size        int64
	IsDir       bool
	Children    []*treeNode
	HiddenFiles int
}

// summarizeTree limits the number of visible files in each directory to maxFiles
// this should cut down on load times and make the report more readable
func summarizeTree(node *treeNode, maxFiles int) {
	if node == nil || !node.IsDir {
		return // exit if the node is nil or not a directory (e.g. a file)
	}

	// Sort child items into arrays for dirs and files
	var dirs []*treeNode
	var files []*treeNode
	for _, child := range node.Children {
		if child.IsDir {
			dirs = append(dirs, child)
			continue
		}
		files = append(files, child)
	}

	// Sort directories and files by size (largest first), then by name (alphabetically)
	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i].Size == dirs[j].Size {
			return dirs[i].Name < dirs[j].Name
		}
		return dirs[i].Size > dirs[j].Size
	})
	sort.Slice(files, func(i, j int) bool {
		if files[i].Size == files[j].Size {
			return files[i].Name < files[j].Name
		}
		return files[i].Size > files[j].Size
	})

	// Create a new slice for visible children, limiting the number of files to maxFiles
	visible := make([]*treeNode, 0, len(dirs)+len(files))
	visible = append(visible, dirs...)
	if len(files) > maxFiles {
		visible = append(visible, files[:maxFiles]...)
		node.HiddenFiles = len(files) - maxFiles
	} else {
		visible = append(visible, files...)
		node.HiddenFiles = 0
	} // Update the node's children to only include the visible ones
	node.Children = visible

	// Recursively summarize the child directories
	for _, child := range node.Children {
		if child.IsDir {
			summarizeTree(child, maxFiles)
		}
	}
}

// sumNodeSize calculates the total size of a directory by summing the sizes of its children
func sumNodeSize(node *treeNode) int64 {
	if !node.IsDir {
		return node.Size
	}
	var total int64
	for _, child := range node.Children {
		total += sumNodeSize(child)
	}
	return total
}

// getAllocatedSize returns the allocated size of a file or directory in bytes.
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

// buildTree recursively builds a treeNode structure representing the directory tree
// (starting from the given root path)
func buildTree(root string) (*treeNode, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return &treeNode{Name: filepath.Base(root), Path: root, Size: getAllocatedSize(root, info), IsDir: false}, nil
	}

	// Create a treeNode for the directory
	node := &treeNode{
		Name:  filepath.Base(root),
		Path:  root,
		IsDir: true,
	}

	// Read the directory entries and handle permission errors gracefully
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsPermission(err) || strings.Contains(err.Error(), "Access is denied") {
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", root, err)
			return node, nil
		}
		return nil, err
	}

	// Iterate over the entries and build the tree recursively
	for _, entry := range entries {
		childPath := filepath.Join(root, entry.Name())

		if entry.IsDir() {
			child, err := buildTree(childPath)
			if err != nil { // Handle errors when scanning child directories
				if os.IsPermission(err) || strings.Contains(err.Error(), "Access is denied") {
					fmt.Fprintf(os.Stderr, "skipping %s: %v\n", childPath, err)
					continue
				}
				fmt.Fprintf(os.Stderr, "error scanning %s: %v\n", childPath, err)
				continue
			}
			node.Children = append(node.Children, child)
			continue
		}

		// For files, get the file info to determine the size and handle permission errors gracefully
		info, err := entry.Info()
		if err != nil {
			if os.IsPermission(err) || strings.Contains(err.Error(), "Access is denied") {
				fmt.Fprintf(os.Stderr, "skipping %s: %v\n", childPath, err)
				continue
			}
			fmt.Fprintf(os.Stderr, "error scanning %s: %v\n", childPath, err)
			continue
		}

		// Append the file node to the children of the current directory node
		node.Children = append(node.Children, &treeNode{
			Name:  entry.Name(),
			Path:  childPath,
			Size:  getAllocatedSize(childPath, info),
			IsDir: false,
		})
	}

	// Calculate the total size of the directory and summarize the tree
	node.Size = sumNodeSize(node)
	summarizeTree(node, maxFilesPerFolder)
	return node, nil
}

// countFiles counts the total number of files
// and their cumulative size in bytes for a given treeNode
func countFiles(node *treeNode) (int, int64) {
	if !node.IsDir {
		return 1, node.Size
	}
	var totalFiles int
	var totalBytes int64
	for _, child := range node.Children {
		files, bytes := countFiles(child)
		totalFiles += files
		totalBytes += bytes
	}
	return totalFiles, totalBytes
}

// humanSize converts a size in bytes to a human-readable string
// with appropriate units (B, KB, MB, GB).
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

// renderNodeHTML generates the HTML representation of a treeNode and its children recursively
func renderNodeHTML(node *treeNode, isRoot bool) string {
	if !node.IsDir {
		return fmt.Sprintf("<li class=\"file\">%s <span class=\"size\">%s</span></li>", html.EscapeString(node.Name), html.EscapeString(humanSize(node.Size)))
	}

	var b strings.Builder
	b.WriteString("<li>")
	openAttr := ""
	if isRoot {
		openAttr = " open"
	}
	b.WriteString(fmt.Sprintf("<details class=\"dir\"%s><summary>%s <span class=\"size\">%s</span></summary><div class=\"children\">", openAttr, html.EscapeString(node.Name), html.EscapeString(humanSize(node.Size))))
	b.WriteString("<ul class=\"tree\">")
	for _, child := range node.Children {
		b.WriteString(renderNodeHTML(child, false))
	}
	if node.HiddenFiles > 0 {
		b.WriteString(fmt.Sprintf("<li class=\"summary\">+ %d more file(s) not shown</li>", node.HiddenFiles))
	}
	b.WriteString("</ul></div></details></li>")
	return b.String()
}

// renderHTML generates an HTML report of the directory tree starting from the root node
func renderHTML(root *treeNode, rootPath string, totalFiles int, totalBytes int64) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>")
	b.WriteString("<html lang=\"en\">")
	b.WriteString("<head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>Directory Report</title>")
	b.WriteString("<style>")
	b.WriteString("body{font-family:Segoe UI, Arial, sans-serif; margin:24px; color:#1f2937; background:#f8fafc;} ")
	b.WriteString("h1{margin-bottom:8px;} .meta{color:#475569; margin-bottom:16px;} .warning{background:#fef3c7; border:1px solid #f59e0b; color:#92400e; padding:12px 14px; border-radius:8px; margin-bottom:16px;} .tree{list-style:none; padding-left:18px;} ")
	b.WriteString("details{margin:4px 0;} summary{cursor:pointer; font-weight:600; padding:2px 4px; border-radius:4px;} summary:hover{background:#e2e8f0;} ")
	b.WriteString("li{margin:4px 0;} .file{color:#334155;} .summary{color:#64748b; font-style:italic;} .size{color:#64748b; margin-left:8px; font-size:0.95em;} .children{margin-left:16px; padding-left:8px; border-left:1px solid #cbd5e1;} ")
	b.WriteString("</style></head><body>")
	b.WriteString(fmt.Sprintf("<h1>Directory report</h1><div class=\"warning\"><strong>Warning:</strong> Results may be incomplete if this report was generated without administrator elevation. Some folders may be skipped or inaccessible.</div><div class=\"meta\"><strong>Root:</strong> %s<br><strong>Files:</strong> %d<br><strong>Total size:</strong> %s</div>", html.EscapeString(rootPath), totalFiles, html.EscapeString(humanSize(totalBytes))))
	b.WriteString("<ul class=\"tree\">")
	b.WriteString(renderNodeHTML(root, true))
	b.WriteString("</ul></body></html>")
	return b.String()
}

// writeReport writes the generated HTML report to the specified file path
func writeReport(path string, contents string) error {
	outputDir := filepath.Dir(path)
	if outputDir != "." && outputDir != "" {
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(contents), 0o644)
}

// main is the entry point of the program.
// It processes command-line arguments, builds the directory tree,
// generates the HTML report, and writes it to a file.

func main() {
	// print usage instructions if the number of arguments is incorrect
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s [root-directory] [output-file]\n", filepath.Base(os.Args[0]))
		fmt.Fprintln(os.Stderr, "  root-directory: folder to scan (e.g. C:\\ or .)")
		fmt.Fprintln(os.Stderr, "  output-file: where to write the HTML report (e.g. .\\report.html)")
		os.Exit(1)
	}

	root := os.Args[1]
	outputPath := os.Args[2]

	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error resolving root:", err)
		os.Exit(1)
	}

	// Build the directory tree starting from the specified root path and handle errors
	tree, err := buildTree(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error scanning:", err)
		os.Exit(1)
	}

	// Count the total number of files and their cumulative size in bytes
	// then generate the HTML report and write it to the specified output path
	totalFiles, totalBytes := countFiles(tree)
	htmlReport := renderHTML(tree, absRoot, totalFiles, totalBytes)
	if err := writeReport(outputPath, htmlReport); err != nil {
		fmt.Fprintln(os.Stderr, "error writing report:", err)
		os.Exit(1)
	}

	// confirmation message
	fmt.Printf("Wrote HTML report to %s\n", outputPath)
	fmt.Printf("Root: %s\n", absRoot)
	fmt.Printf("Files: %d\nTotal size: %s\n", totalFiles, humanSize(totalBytes))
}

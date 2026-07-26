package project

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ignoredDirectories = map[string]bool{
	".git": true, ".idea": true, ".vscode": true, "node_modules": true,
	"dist": true, "build": true, "target": true, "vendor": true, "coverage": true,
}

var languages = map[string]struct{ name, color string }{
	".go": {"Go", "#38bdf8"}, ".rs": {"Rust", "#f97316"},
	".ts": {"TypeScript", "#3b82f6"}, ".tsx": {"TypeScript", "#3b82f6"},
	".js": {"JavaScript", "#eab308"}, ".jsx": {"JavaScript", "#eab308"},
	".py": {"Python", "#22c55e"}, ".java": {"Java", "#ef4444"},
	".cs": {"C#", "#a855f7"}, ".cpp": {"C++", "#6366f1"}, ".c": {"C", "#94a3b8"},
	".rb": {"Ruby", "#dc2626"}, ".php": {"PHP", "#818cf8"},
	".html": {"HTML", "#fb7185"}, ".css": {"CSS", "#8b5cf6"},
	".md": {"Markdown", "#64748b"}, ".yaml": {"YAML", "#f59e0b"}, ".yml": {"YAML", "#f59e0b"},
}

type Scanner struct {
	root string
}

func NewScanner(root string) *Scanner {
	return &Scanner{root: root}
}

func (s *Scanner) Root() string { return s.root }

func (s *Scanner) Scan(ctx context.Context) (Snapshot, error) {
	start := time.Now()
	snapshot := Snapshot{
		Project:   ProjectInfo{Name: filepath.Base(s.root), Path: s.root},
		Modules:   make([]Module, 0),
		Changes:   make([]Change, 0),
		Languages: make([]Language, 0),
	}
	type languageCount struct {
		files int
		lines int
	}
	counts := map[string]*languageCount{}
	modules := map[string]*Module{}

	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrPermission) {
				return nil
			}
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			if path != s.root && ignoredDirectories[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		relative, err := filepath.Rel(s.root, path)
		if err != nil {
			return nil
		}
		snapshot.Summary.Files++
		extension := strings.ToLower(filepath.Ext(path))
		language, source := languages[extension]
		if !source {
			return nil
		}
		lines, err := countLines(path)
		if err != nil {
			return nil
		}
		snapshot.Summary.SourceFiles++
		snapshot.Summary.CodeLines += lines
		count := counts[language.name]
		if count == nil {
			count = &languageCount{}
			counts[language.name] = count
		}
		count.files++
		count.lines += lines

		moduleName, modulePath := moduleFor(relative)
		module := modules[modulePath]
		if module == nil {
			module = &Module{
				Name: moduleName, Path: modulePath, Description: describeModule(moduleName),
				Status: "healthy",
			}
			modules[modulePath] = module
		}
		module.Files++
		module.CodeLines += lines
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}

	snapshot.Project.Branch, snapshot.Project.IsGit = s.gitBranch(ctx)
	snapshot.Changes = s.gitChanges(ctx)
	if snapshot.Changes == nil {
		snapshot.Changes = make([]Change, 0)
	}
	for _, change := range snapshot.Changes {
		snapshot.Summary.Insertions += change.Insertions
		snapshot.Summary.Deletions += change.Deletions
	}
	snapshot.Summary.Changes = len(snapshot.Changes)

	for _, module := range modules {
		snapshot.Modules = append(snapshot.Modules, *module)
	}
	sort.Slice(snapshot.Modules, func(i, j int) bool {
		if snapshot.Modules[i].CodeLines == snapshot.Modules[j].CodeLines {
			return snapshot.Modules[i].Path < snapshot.Modules[j].Path
		}
		return snapshot.Modules[i].CodeLines > snapshot.Modules[j].CodeLines
	})
	if len(snapshot.Modules) > 8 {
		snapshot.Modules = snapshot.Modules[:8]
	}
	snapshot.Summary.Modules = len(modules)

	for name, count := range counts {
		color := "#64748b"
		for _, language := range languages {
			if language.name == name {
				color = language.color
				break
			}
		}
		percentage := 0.0
		if snapshot.Summary.CodeLines > 0 {
			percentage = float64(count.lines) / float64(snapshot.Summary.CodeLines) * 100
		}
		snapshot.Languages = append(snapshot.Languages, Language{
			Name: name, Files: count.files, Lines: count.lines,
			Percentage: percentage, Color: color,
		})
	}
	sort.Slice(snapshot.Languages, func(i, j int) bool {
		return snapshot.Languages[i].Lines > snapshot.Languages[j].Lines
	})

	snapshot.ScannedAt = time.Now()
	snapshot.Summary.LastScanMS = time.Since(start).Milliseconds()
	return snapshot, nil
}

func countLines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	lines := 0
	for {
		content, err := reader.ReadString('\n')
		if err == nil {
			lines++
			continue
		}
		if errors.Is(err, io.EOF) {
			if len(content) > 0 {
				lines++
			}
			break
		}
		return lines, err
	}
	return lines, nil
}

func moduleFor(relative string) (string, string) {
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) == 1 {
		return "root", "."
	}
	if (parts[0] == "src" || parts[0] == "internal" || parts[0] == "pkg" || parts[0] == "cmd") && len(parts) > 2 {
		return parts[1], parts[0] + "/" + parts[1]
	}
	return parts[0], parts[0]
}

func describeModule(name string) string {
	switch strings.ToLower(name) {
	case "cmd":
		return "Application entry points"
	case "internal":
		return "Private application packages"
	case "src":
		return "Application source"
	case "test", "tests":
		return "Automated test suites"
	case "docs":
		return "Project documentation"
	case "root":
		return "Repository configuration"
	default:
		return "Repository module"
	}
}

func (s *Scanner) gitBranch(ctx context.Context) (string, bool) {
	output, err := s.git(ctx, "branch", "--show-current")
	if err != nil {
		return "", false
	}
	branch := strings.TrimSpace(string(output))
	if branch == "" {
		branch = "detached"
	}
	return branch, true
}

func (s *Scanner) gitChanges(ctx context.Context) []Change {
	statusOutput, err := s.git(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil
	}
	stats := s.gitDiffStats(ctx)
	var changes []Change
	records := bytes.Split(statusOutput, []byte{0})
	for index := 0; index < len(records); index++ {
		record := records[index]
		if len(record) < 4 {
			continue
		}
		code := string(record[:2])
		path := string(record[3:])
		if code[0] == 'R' || code[0] == 'C' {
			index++
			if index < len(records) && len(records[index]) > 0 {
				path = string(records[index])
			}
		}
		change := Change{Path: filepath.ToSlash(path), Status: changeStatus(code)}
		if stat, ok := stats[path]; ok {
			change.Insertions, change.Deletions = stat[0], stat[1]
		}
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	if len(changes) > 12 {
		changes = changes[:12]
	}
	return changes
}

func (s *Scanner) gitDiffStats(ctx context.Context) map[string][2]int {
	result := map[string][2]int{}
	for _, args := range [][]string{
		{"diff", "--numstat"}, {"diff", "--cached", "--numstat"},
	} {
		output, err := s.git(ctx, args...)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(bytes.NewReader(output))
		for scanner.Scan() {
			parts := strings.SplitN(scanner.Text(), "\t", 3)
			if len(parts) != 3 {
				continue
			}
			added, _ := strconv.Atoi(parts[0])
			deleted, _ := strconv.Atoi(parts[1])
			current := result[parts[2]]
			result[parts[2]] = [2]int{current[0] + added, current[1] + deleted}
		}
	}
	return result
}

func (s *Scanner) git(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", s.root}, args...)...)
	command.Stderr = io.Discard
	return command.Output()
}

func changeStatus(code string) string {
	switch {
	case strings.Contains(code, "?"):
		return "untracked"
	case strings.Contains(code, "A"):
		return "added"
	case strings.Contains(code, "D"):
		return "deleted"
	case strings.Contains(code, "R"):
		return "renamed"
	default:
		return "modified"
	}
}

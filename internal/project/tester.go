package project

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	testTimeout   = 2 * time.Minute
	maxTestOutput = 256 * 1024
)

type TestState struct {
	Available  bool          `json:"available"`
	Framework  string        `json:"framework,omitempty"`
	Command    string        `json:"command,omitempty"`
	Status     string        `json:"status"`
	Packages   []TestPackage `json:"packages"`
	StartedAt  *time.Time    `json:"startedAt,omitempty"`
	FinishedAt *time.Time    `json:"finishedAt,omitempty"`
	DurationMS int64         `json:"durationMs"`
	Output     string        `json:"output,omitempty"`
	Error      string        `json:"error,omitempty"`
}

type TestPackage struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"durationMs"`
}

type testFramework interface {
	detect(root string) bool
	name() string
	command(root string) (string, []string)
	parse([]byte) ([]TestPackage, string)
}

var testFrameworks = []testFramework{
	goTestFramework{},
	pythonUnittestFramework{},
	phpUnitFramework{},
}

type TestRunner struct {
	root      string
	framework testFramework
	mu        sync.RWMutex
	state     TestState
	cancel    context.CancelFunc
}

func NewTestRunner(root string) *TestRunner {
	runner := &TestRunner{root: root}
	for _, framework := range testFrameworks {
		if framework.detect(root) {
			runner.framework = framework
			break
		}
	}
	runner.state = runner.initialState()
	return runner
}

func (r *TestRunner) State() TestState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state := r.state
	state.Packages = append([]TestPackage(nil), state.Packages...)
	return state
}

func (r *TestRunner) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.framework == nil {
		return errors.New("no supported test framework detected")
	}
	if r.state.Status == "running" {
		return errors.New("tests are already running")
	}
	executable, arguments := r.framework.command(r.root)
	started := time.Now()
	r.state = TestState{
		Available: true, Framework: r.framework.name(),
		Command: formatTestCommand(executable, arguments), Status: "running",
		Packages: make([]TestPackage, 0), StartedAt: &started,
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	r.cancel = cancel
	go r.run(ctx, started, executable, arguments)
	return nil
}

func (r *TestRunner) Cancel() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.Status != "running" || r.cancel == nil {
		return errors.New("no test run is active")
	}
	r.cancel()
	return nil
}

func (r *TestRunner) initialState() TestState {
	state := TestState{Status: "idle", Packages: make([]TestPackage, 0)}
	if r.framework != nil {
		executable, arguments := r.framework.command(r.root)
		state.Available = true
		state.Framework = r.framework.name()
		state.Command = formatTestCommand(executable, arguments)
	}
	return state
}

func (r *TestRunner) run(ctx context.Context, started time.Time, executable string, arguments []string) {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = r.root
	output, commandErr := command.CombinedOutput()
	finished := time.Now()
	packages, readableOutput := r.framework.parse(output)
	state := TestState{
		Available: true, Framework: r.framework.name(),
		Command: formatTestCommand(executable, arguments), Status: "passed",
		Packages: packages, StartedAt: &started, FinishedAt: &finished,
		DurationMS: finished.Sub(started).Milliseconds(), Output: trimTestOutput([]byte(readableOutput)),
	}
	if commandErr != nil {
		state.Status = "failed"
		state.Error = commandErr.Error()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		state.Status = "timeout"
		state.Error = "test run exceeded the 2 minute timeout"
	} else if errors.Is(ctx.Err(), context.Canceled) {
		state.Status = "canceled"
		state.Error = "test run was canceled"
	}

	r.mu.Lock()
	r.state = state
	r.cancel = nil
	r.mu.Unlock()
}

func formatTestCommand(executable string, arguments []string) string {
	return strings.Join(append([]string{filepath.ToSlash(executable)}, arguments...), " ")
}

type goTestFramework struct{}

func (goTestFramework) detect(root string) bool {
	return fileExists(filepath.Join(root, "go.mod"))
}

func (goTestFramework) name() string { return "Go" }

func (goTestFramework) command(string) (string, []string) {
	return "go", []string{"test", "-json", "./..."}
}

func (goTestFramework) parse(output []byte) ([]TestPackage, string) {
	type event struct {
		Action  string  `json:"Action"`
		Package string  `json:"Package"`
		Elapsed float64 `json:"Elapsed"`
		Output  string  `json:"Output"`
	}
	results := make(map[string]TestPackage)
	var readable bytes.Buffer
	scanner := testOutputScanner(output)
	for scanner.Scan() {
		var item event
		if json.Unmarshal(scanner.Bytes(), &item) != nil {
			readable.Write(scanner.Bytes())
			readable.WriteByte('\n')
			continue
		}
		readable.WriteString(item.Output)
		if item.Package != "" && (item.Action == "pass" || item.Action == "fail" || item.Action == "skip") {
			results[item.Package] = TestPackage{
				Name: item.Package, Status: item.Action,
				DurationMS: int64(item.Elapsed * 1000),
			}
		}
	}
	return sortedTestResults(results), readable.String()
}

type pythonUnittestFramework struct{}

func (pythonUnittestFramework) detect(root string) bool {
	isPythonProject := fileExists(filepath.Join(root, "pyproject.toml")) ||
		fileExists(filepath.Join(root, "requirements.txt")) ||
		fileExists(filepath.Join(root, "setup.py"))
	return isPythonProject && hasPythonTests(root)
}

func (pythonUnittestFramework) name() string { return "Python unittest" }

func (pythonUnittestFramework) command(root string) (string, []string) {
	python := "python3"
	for _, candidate := range []string{
		filepath.Join(root, ".venv", "bin", "python"),
		filepath.Join(root, "venv", "bin", "python"),
	} {
		if fileExists(candidate) {
			python = candidate
			break
		}
	}
	start := "."
	if info, err := os.Stat(filepath.Join(root, "tests")); err == nil && info.IsDir() {
		start = "tests"
	} else if info, err := os.Stat(filepath.Join(root, "test")); err == nil && info.IsDir() {
		start = "test"
	}
	return python, []string{"-m", "unittest", "discover", "-s", start, "-p", "test_*.py", "-v"}
}

var pythonTestLine = regexp.MustCompile(`^(.*?) \((.*?)\) \.\.\. (ok|FAIL|ERROR|skipped .*)$`)

func (pythonUnittestFramework) parse(output []byte) ([]TestPackage, string) {
	results := make(map[string]TestPackage)
	scanner := testOutputScanner(output)
	for scanner.Scan() {
		match := pythonTestLine.FindStringSubmatch(strings.TrimSpace(scanner.Text()))
		if len(match) == 4 {
			status := "pass"
			if match[3] == "FAIL" || match[3] == "ERROR" {
				status = "fail"
			} else if strings.HasPrefix(match[3], "skipped ") {
				status = "skip"
			}
			name := fmt.Sprintf("%s.%s", match[2], match[1])
			results[name] = TestPackage{Name: name, Status: status}
		}
	}
	return sortedTestResults(results), string(output)
}

type phpUnitFramework struct{}

func (phpUnitFramework) detect(root string) bool {
	if fileExists(filepath.Join(root, "phpunit.xml")) || fileExists(filepath.Join(root, "phpunit.xml.dist")) {
		return true
	}
	composer, err := os.ReadFile(filepath.Join(root, "composer.json"))
	return err == nil && bytes.Contains(bytes.ToLower(composer), []byte("phpunit"))
}

func (phpUnitFramework) name() string { return "PHPUnit" }

func (phpUnitFramework) command(root string) (string, []string) {
	local := filepath.Join(root, "vendor", "bin", "phpunit")
	if fileExists(local) {
		return local, []string{"--testdox", "--colors=never"}
	}
	return "phpunit", []string{"--testdox", "--colors=never"}
}

var phpUnitSummary = regexp.MustCompile(`(?i)Tests:\s*(\d+)(?:,\s*Assertions:\s*\d+)?(?:,\s*(?:Failures|Errors):\s*(\d+))?`)

func (phpUnitFramework) parse(output []byte) ([]TestPackage, string) {
	match := phpUnitSummary.FindSubmatch(output)
	if len(match) == 0 {
		return make([]TestPackage, 0), string(output)
	}
	total, _ := strconv.Atoi(string(match[1]))
	failed := 0
	if len(match) > 2 {
		failed, _ = strconv.Atoi(string(match[2]))
	}
	results := make([]TestPackage, 0, 2)
	if total-failed > 0 {
		results = append(results, TestPackage{Name: "Successful tests", Status: "pass", DurationMS: 0})
	}
	if failed > 0 {
		results = append(results, TestPackage{Name: "Failed tests", Status: "fail", DurationMS: 0})
	}
	return results, string(output)
}

func testOutputScanner(output []byte) *bufio.Scanner {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	return scanner
}

func sortedTestResults(results map[string]TestPackage) []TestPackage {
	items := make([]TestPackage, 0, len(results))
	for _, item := range results {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func hasPythonTests(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != root && ignoredDirectories[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func trimTestOutput(output []byte) string {
	if len(output) <= maxTestOutput {
		return string(output)
	}
	return "[output truncated]\n" + string(output[len(output)-maxTestOutput:])
}

package project

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
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
	maxTestRuns   = 20
)

type TestState struct {
	Status string      `json:"status"`
	Suites []TestSuite `json:"suites"`
}

type TestSuite struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Path       string       `json:"path"`
	Framework  string       `json:"framework"`
	Command    string       `json:"command"`
	Status     string       `json:"status"`
	Results    []TestResult `json:"results"`
	StartedAt  *time.Time   `json:"startedAt,omitempty"`
	FinishedAt *time.Time   `json:"finishedAt,omitempty"`
	DurationMS int64        `json:"durationMs"`
	Output     string       `json:"output,omitempty"`
	Error      string       `json:"error,omitempty"`
	Stale      bool         `json:"stale"`
	Changed    int          `json:"changedFiles"`
	History    []TestRun    `json:"history"`
}

type TestResult struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"durationMs"`
}

type TestRun struct {
	Status      string       `json:"status"`
	StartedAt   time.Time    `json:"startedAt"`
	FinishedAt  time.Time    `json:"finishedAt"`
	DurationMS  int64        `json:"durationMs"`
	Results     []TestResult `json:"results"`
	Output      string       `json:"output,omitempty"`
	Error       string       `json:"error,omitempty"`
	Fingerprint string       `json:"fingerprint"`
	Slow        bool         `json:"slow,omitempty"`
}

type testFramework interface {
	detect(root string) bool
	name() string
	command(root string) (string, []string)
	parse([]byte) ([]TestResult, string)
	relevant(string) bool
}

var testFrameworks = []testFramework{
	goTestFramework{},
	pythonUnittestFramework{},
	phpUnitFramework{},
	javaScriptTestFramework{},
}

type testSuiteRuntime struct {
	suite     TestSuite
	framework testFramework
	cancel    context.CancelFunc
	baseline  map[string]fileSignature
}

type fileSignature struct {
	size    int64
	modTime int64
}

type TestRunner struct {
	root        string
	historyPath string
	mu          sync.RWMutex
	suites      map[string]*testSuiteRuntime
	history     map[string][]TestRun
}

func NewTestRunner(root string) *TestRunner {
	runner := &TestRunner{
		root: root, historyPath: testHistoryPath(root),
		suites: make(map[string]*testSuiteRuntime), history: make(map[string][]TestRun),
	}
	runner.loadHistory()
	runner.Refresh()
	return runner
}

func (r *TestRunner) Refresh() {
	detected := discoverTestSuites(r.root)
	r.mu.Lock()
	defer r.mu.Unlock()

	current := make(map[string]bool, len(detected))
	for _, candidate := range detected {
		current[candidate.suite.ID] = true
		existing := r.suites[candidate.suite.ID]
		if existing == nil {
			candidate.suite.History = append([]TestRun(nil), r.history[candidate.suite.ID]...)
			r.suites[candidate.suite.ID] = candidate
			continue
		}
		if existing.suite.Status != "running" {
			existing.framework = candidate.framework
			existing.suite.Name = candidate.suite.Name
			existing.suite.Path = candidate.suite.Path
			existing.suite.Framework = candidate.suite.Framework
			existing.suite.Command = candidate.suite.Command
		}
		if existing.suite.StartedAt != nil {
			currentFiles := relevantFileSignatures(
				filepath.Join(r.root, filepath.FromSlash(existing.suite.Path)),
				existing.framework,
			)
			existing.suite.Changed = changedFileCount(existing.baseline, currentFiles)
			existing.suite.Stale = existing.suite.Changed > 0
		}
	}
	for id, runtime := range r.suites {
		if !current[id] && runtime.suite.Status != "running" {
			delete(r.suites, id)
		}
	}
}

func (r *TestRunner) State() TestState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state := TestState{Status: "idle", Suites: make([]TestSuite, 0, len(r.suites))}
	hasPassed, hasFailed, hasCanceled := false, false, false
	for _, runtime := range r.suites {
		suite := runtime.suite
		suite.Results = append([]TestResult(nil), suite.Results...)
		suite.History = cloneTestRuns(suite.History)
		state.Suites = append(state.Suites, suite)
		switch suite.Status {
		case "running":
			state.Status = "running"
		case "failed", "timeout":
			hasFailed = true
		case "passed":
			hasPassed = true
		case "canceled":
			hasCanceled = true
		}
	}
	if state.Status != "running" {
		if hasFailed {
			state.Status = "failed"
		} else if hasPassed {
			state.Status = "passed"
		} else if hasCanceled {
			state.Status = "canceled"
		}
	}
	sort.Slice(state.Suites, func(i, j int) bool {
		if state.Suites[i].Path == state.Suites[j].Path {
			return state.Suites[i].Framework < state.Suites[j].Framework
		}
		return state.Suites[i].Path < state.Suites[j].Path
	})
	return state
}

func (r *TestRunner) Start(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	targets, err := r.targets(id)
	if err != nil {
		return err
	}
	started := 0
	for _, runtime := range targets {
		if runtime.suite.Status == "running" {
			continue
		}
		r.startLocked(runtime)
		started++
	}
	if started == 0 {
		return errors.New("selected tests are already running")
	}
	return nil
}

func (r *TestRunner) Cancel(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	targets, err := r.targets(id)
	if err != nil {
		return err
	}
	canceled := 0
	for _, runtime := range targets {
		if runtime.suite.Status == "running" && runtime.cancel != nil {
			runtime.cancel()
			canceled++
		}
	}
	if canceled == 0 {
		return errors.New("no matching test run is active")
	}
	return nil
}

func (r *TestRunner) targets(id string) ([]*testSuiteRuntime, error) {
	if id != "" {
		runtime := r.suites[id]
		if runtime == nil {
			return nil, errors.New("test suite not found")
		}
		return []*testSuiteRuntime{runtime}, nil
	}
	if len(r.suites) == 0 {
		return nil, errors.New("no supported test suites detected")
	}
	targets := make([]*testSuiteRuntime, 0, len(r.suites))
	for _, runtime := range r.suites {
		targets = append(targets, runtime)
	}
	return targets, nil
}

func (r *TestRunner) startLocked(runtime *testSuiteRuntime) {
	executable, arguments := runtime.framework.command(filepath.Join(r.root, filepath.FromSlash(runtime.suite.Path)))
	started := time.Now()
	runtime.suite.Status = "running"
	runtime.suite.Results = make([]TestResult, 0)
	runtime.suite.StartedAt = &started
	runtime.suite.FinishedAt = nil
	runtime.suite.DurationMS = 0
	runtime.suite.Output = ""
	runtime.suite.Error = ""
	runtime.suite.Stale = false
	runtime.suite.Changed = 0
	runtime.baseline = relevantFileSignatures(
		filepath.Join(r.root, filepath.FromSlash(runtime.suite.Path)),
		runtime.framework,
	)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	runtime.cancel = cancel
	go r.run(ctx, runtime.suite.ID, started, executable, arguments)
}

func (r *TestRunner) run(ctx context.Context, id string, started time.Time, executable string, arguments []string) {
	r.mu.RLock()
	runtime := r.suites[id]
	workingDirectory := filepath.Join(r.root, filepath.FromSlash(runtime.suite.Path))
	framework := runtime.framework
	r.mu.RUnlock()

	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = workingDirectory
	command.Env = append(os.Environ(), "CI=1")
	output, commandErr := command.CombinedOutput()
	finished := time.Now()
	results, readableOutput := framework.parse(output)
	status := "passed"
	errorMessage := ""
	if commandErr != nil {
		status = "failed"
		errorMessage = commandErr.Error()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status = "timeout"
		errorMessage = "test run exceeded the 2 minute timeout"
	} else if errors.Is(ctx.Err(), context.Canceled) {
		status = "canceled"
		errorMessage = "test run was canceled"
	}
	if len(results) == 0 {
		results = []TestResult{{Name: framework.name(), Status: resultStatus(status)}}
	}

	r.mu.Lock()
	runtime = r.suites[id]
	runtime.suite.Status = status
	runtime.suite.Results = results
	runtime.suite.FinishedAt = &finished
	runtime.suite.DurationMS = finished.Sub(started).Milliseconds()
	runtime.suite.Output = trimTestOutput([]byte(readableOutput))
	runtime.suite.Error = errorMessage
	run := TestRun{
		Status: status, StartedAt: started, FinishedAt: finished,
		DurationMS: runtime.suite.DurationMS, Results: append([]TestResult(nil), results...),
		Error: errorMessage, Fingerprint: signatureFingerprint(runtime.baseline),
	}
	run.Slow = status == "passed" && testRunIsSlower(run.DurationMS, runtime.suite.History)
	if status != "passed" {
		run.Output = runtime.suite.Output
	}
	runtime.suite.History = append([]TestRun{run}, runtime.suite.History...)
	if len(runtime.suite.History) > maxTestRuns {
		runtime.suite.History = runtime.suite.History[:maxTestRuns]
	}
	r.history[id] = cloneTestRuns(runtime.suite.History)
	runtime.cancel = nil
	r.saveHistoryLocked()
	r.mu.Unlock()
}

func resultStatus(status string) string {
	if status == "passed" {
		return "pass"
	}
	if status == "canceled" {
		return "skip"
	}
	return "fail"
}

func discoverTestSuites(root string) []*testSuiteRuntime {
	var suites []*testSuiteRuntime
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && ignoredDirectories[entry.Name()] {
			return filepath.SkipDir
		}
		for _, framework := range testFrameworks {
			if !framework.detect(path) {
				continue
			}
			relative, relativeErr := filepath.Rel(root, path)
			if relativeErr != nil {
				continue
			}
			relative = filepath.ToSlash(relative)
			name := filepath.Base(path)
			if relative == "." {
				name = filepath.Base(root)
			}
			executable, arguments := framework.command(path)
			id := strings.ToLower(strings.ReplaceAll(framework.name(), " ", "-")) + ":" + relative
			suites = append(suites, &testSuiteRuntime{
				framework: framework,
				suite: TestSuite{
					ID: id, Name: name, Path: relative, Framework: framework.name(),
					Command: formatTestCommand(executable, arguments), Status: "idle",
					Results: make([]TestResult, 0), History: make([]TestRun, 0),
				},
			})
		}
		return nil
	})
	return suites
}

func formatTestCommand(executable string, arguments []string) string {
	return strings.Join(append([]string{filepath.ToSlash(executable)}, arguments...), " ")
}

type goTestFramework struct{}

func (goTestFramework) detect(root string) bool { return fileExists(filepath.Join(root, "go.mod")) }
func (goTestFramework) name() string            { return "Go" }
func (goTestFramework) command(string) (string, []string) {
	return "go", []string{"test", "-json", "./..."}
}
func (goTestFramework) parse(output []byte) ([]TestResult, string) {
	type event struct {
		Action  string  `json:"Action"`
		Package string  `json:"Package"`
		Elapsed float64 `json:"Elapsed"`
		Output  string  `json:"Output"`
	}
	results := make(map[string]TestResult)
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
			results[item.Package] = TestResult{Name: item.Package, Status: item.Action, DurationMS: int64(item.Elapsed * 1000)}
		}
	}
	return sortedTestResults(results), readable.String()
}
func (goTestFramework) relevant(path string) bool {
	name := filepath.Base(path)
	return strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.sum"
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
	for _, candidate := range []string{filepath.Join(root, ".venv", "bin", "python"), filepath.Join(root, "venv", "bin", "python")} {
		if fileExists(candidate) {
			python = candidate
			break
		}
	}
	start := "."
	if directoryExists(filepath.Join(root, "tests")) {
		start = "tests"
	} else if directoryExists(filepath.Join(root, "test")) {
		start = "test"
	}
	return python, []string{"-m", "unittest", "discover", "-s", start, "-p", "test_*.py", "-v"}
}

var pythonTestLine = regexp.MustCompile(`^(.*?) \((.*?)\) \.\.\. (ok|FAIL|ERROR|skipped .*)$`)

func (pythonUnittestFramework) parse(output []byte) ([]TestResult, string) {
	results := make(map[string]TestResult)
	scanner := testOutputScanner(output)
	for scanner.Scan() {
		match := pythonTestLine.FindStringSubmatch(strings.TrimSpace(scanner.Text()))
		if len(match) != 4 {
			continue
		}
		status := "pass"
		if match[3] == "FAIL" || match[3] == "ERROR" {
			status = "fail"
		} else if strings.HasPrefix(match[3], "skipped ") {
			status = "skip"
		}
		name := fmt.Sprintf("%s.%s", match[2], match[1])
		results[name] = TestResult{Name: name, Status: status}
	}
	return sortedTestResults(results), string(output)
}
func (pythonUnittestFramework) relevant(path string) bool {
	name := filepath.Base(path)
	return strings.HasSuffix(name, ".py") || name == "pyproject.toml" ||
		name == "requirements.txt" || name == "setup.py" || name == "setup.cfg"
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

func (phpUnitFramework) parse(output []byte) ([]TestResult, string) {
	match := phpUnitSummary.FindSubmatch(output)
	if len(match) == 0 {
		return make([]TestResult, 0), string(output)
	}
	total, _ := strconv.Atoi(string(match[1]))
	failed := 0
	if len(match) > 2 {
		failed, _ = strconv.Atoi(string(match[2]))
	}
	results := make([]TestResult, 0, 2)
	if total-failed > 0 {
		results = append(results, TestResult{Name: fmt.Sprintf("%d successful tests", total-failed), Status: "pass"})
	}
	if failed > 0 {
		results = append(results, TestResult{Name: fmt.Sprintf("%d failed tests", failed), Status: "fail"})
	}
	return results, string(output)
}
func (phpUnitFramework) relevant(path string) bool {
	name := filepath.Base(path)
	return strings.HasSuffix(name, ".php") || name == "composer.json" || name == "composer.lock" ||
		name == "phpunit.xml" || name == "phpunit.xml.dist"
}

type javaScriptTestFramework struct{}

func (javaScriptTestFramework) detect(root string) bool {
	content, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return false
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	return json.Unmarshal(content, &manifest) == nil && strings.TrimSpace(manifest.Scripts["test"]) != ""
}
func (javaScriptTestFramework) name() string { return "JavaScript / TypeScript" }
func (javaScriptTestFramework) command(root string) (string, []string) {
	switch {
	case fileExists(filepath.Join(root, "pnpm-lock.yaml")):
		return "pnpm", []string{"test"}
	case fileExists(filepath.Join(root, "yarn.lock")):
		return "yarn", []string{"test"}
	case fileExists(filepath.Join(root, "bun.lock")), fileExists(filepath.Join(root, "bun.lockb")):
		return "bun", []string{"test"}
	default:
		return "npm", []string{"test"}
	}
}
func (javaScriptTestFramework) parse(output []byte) ([]TestResult, string) {
	return make([]TestResult, 0), string(output)
}
func (javaScriptTestFramework) relevant(path string) bool {
	name := filepath.Base(path)
	switch strings.ToLower(filepath.Ext(name)) {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".vue", ".svelte":
		return true
	}
	switch name {
	case "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb":
		return true
	default:
		return false
	}
}

func testOutputScanner(output []byte) *bufio.Scanner {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	return scanner
}

func sortedTestResults(results map[string]TestResult) []TestResult {
	items := make([]TestResult, 0, len(results))
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

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
		if strings.HasPrefix(entry.Name(), "test_") && strings.HasSuffix(entry.Name(), ".py") {
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

func relevantFileSignatures(root string, framework testFramework) map[string]fileSignature {
	files := make(map[string]fileSignature)
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
		if !framework.relevant(path) {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr == nil {
			files[filepath.ToSlash(relative)] = fileSignature{size: info.Size(), modTime: info.ModTime().UnixNano()}
		}
		return nil
	})
	return files
}

func changedFileCount(baseline, current map[string]fileSignature) int {
	changed := 0
	for path, before := range baseline {
		if after, ok := current[path]; !ok || after != before {
			changed++
		}
	}
	for path := range current {
		if _, ok := baseline[path]; !ok {
			changed++
		}
	}
	return changed
}

func signatureFingerprint(files map[string]fileSignature) string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		signature := files[path]
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", path, signature.size, signature.modTime)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func testHistoryPath(root string) string {
	gitDirectory := filepath.Join(root, ".git")
	if info, err := os.Stat(gitDirectory); err == nil && info.IsDir() {
		return filepath.Join(gitDirectory, "titancode", "test-history.json")
	}
	command := exec.Command("git", "-C", root, "rev-parse", "--git-path", "titancode/test-history.json")
	output, err := command.Output()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(output))
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return filepath.Clean(path)
}

func (r *TestRunner) loadHistory() {
	if r.historyPath == "" {
		return
	}
	content, err := os.ReadFile(r.historyPath)
	if err != nil {
		return
	}
	var history map[string][]TestRun
	if json.Unmarshal(content, &history) != nil {
		return
	}
	for id, runs := range history {
		if len(runs) > maxTestRuns {
			runs = runs[:maxTestRuns]
		}
		r.history[id] = cloneTestRuns(runs)
	}
}

func (r *TestRunner) saveHistoryLocked() {
	if r.historyPath == "" {
		return
	}
	directory := filepath.Dir(r.historyPath)
	if os.MkdirAll(directory, 0o700) != nil {
		return
	}
	content, err := json.MarshalIndent(r.history, "", "  ")
	if err != nil {
		return
	}
	file, err := os.CreateTemp(directory, "test-history-*.tmp")
	if err != nil {
		return
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if file.Chmod(0o600) != nil {
		_ = file.Close()
		return
	}
	if _, err = file.Write(content); err != nil {
		_ = file.Close()
		return
	}
	if file.Close() != nil {
		return
	}
	_ = os.Rename(temporaryPath, r.historyPath)
}

func cloneTestRuns(runs []TestRun) []TestRun {
	cloned := append([]TestRun(nil), runs...)
	for index := range cloned {
		cloned[index].Results = append([]TestResult(nil), cloned[index].Results...)
	}
	if cloned == nil {
		return make([]TestRun, 0)
	}
	return cloned
}

func testRunIsSlower(durationMS int64, history []TestRun) bool {
	var total int64
	count := 0
	for _, run := range history {
		if run.Status != "passed" || run.DurationMS <= 0 {
			continue
		}
		total += run.DurationMS
		count++
		if count == 5 {
			break
		}
	}
	if count == 0 {
		return false
	}
	average := total / int64(count)
	return durationMS-average >= 100 && durationMS*100 >= average*125
}

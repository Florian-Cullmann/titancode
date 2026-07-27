package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerDetectsGoProject(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := NewTestRunner(root).State()
	if len(state.Suites) != 1 || state.Suites[0].Framework != "Go" || state.Status != "idle" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestRunnerLeavesUnsupportedProjectUnavailable(t *testing.T) {
	state := NewTestRunner(t.TempDir()).State()
	if len(state.Suites) != 0 {
		t.Fatalf("unexpected available state: %#v", state)
	}
}

func TestGoFrameworkParsesPackages(t *testing.T) {
	output := []byte(
		"{\"Action\":\"pass\",\"Package\":\"example.com/project/alpha\",\"Elapsed\":0.12}\n" +
			"{\"Action\":\"fail\",\"Package\":\"example.com/project/beta\",\"Elapsed\":0.34}\n",
	)

	packages, _ := (goTestFramework{}).parse(output)
	if len(packages) != 2 {
		t.Fatalf("packages = %#v", packages)
	}
	if packages[0].Status != "pass" || packages[0].DurationMS != 120 {
		t.Fatalf("first package = %#v", packages[0])
	}
	if packages[1].Status != "fail" || packages[1].DurationMS != 340 {
		t.Fatalf("second package = %#v", packages[1])
	}
}

func TestTrimTestOutputExtractsReadableOutput(t *testing.T) {
	output := []byte("--- FAIL: TestExample (0.00s)\n")
	if got := trimTestOutput(output); got != string(output) {
		t.Fatalf("output = %q", got)
	}
}

func TestRunnerDetectsPythonProjectAndVirtualEnvironment(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "requirements.txt"), "fastapi\n")
	writeTestFile(t, filepath.Join(root, "tests", "test_service.py"), "")
	python := filepath.Join(root, ".venv", "bin", "python")
	writeTestFile(t, python, "")

	state := NewTestRunner(root).State()
	if len(state.Suites) != 1 || state.Suites[0].Framework != "Python unittest" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if !strings.Contains(state.Suites[0].Command, ".venv/bin/python -m unittest discover -s tests -p test_*.py -v") {
		t.Fatalf("command = %q", state.Suites[0].Command)
	}
}

func TestRunnerDoesNotOfferPythonTestsWithoutTestFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "requirements.txt"), "fastapi\n")

	state := NewTestRunner(root).State()
	if len(state.Suites) != 0 {
		t.Fatalf("unexpected available state: %#v", state)
	}
}

func TestPythonFrameworkParsesUnittestResults(t *testing.T) {
	output := []byte(
		"test_success (tests.test_service.ServiceTests.test_success) ... ok\n" +
			"test_failure (tests.test_service.ServiceTests.test_failure) ... FAIL\n",
	)

	results, _ := (pythonUnittestFramework{}).parse(output)
	if len(results) != 2 || results[0].Status != "fail" || results[1].Status != "pass" {
		t.Fatalf("results = %#v", results)
	}
}

func TestRunnerDetectsPHPUnitProject(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "composer.json"), `{"require-dev":{"phpunit/phpunit":"^11"}}`)

	state := NewTestRunner(root).State()
	if len(state.Suites) != 1 || state.Suites[0].Framework != "PHPUnit" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if state.Suites[0].Command != "phpunit --testdox --colors=never" {
		t.Fatalf("command = %q", state.Suites[0].Command)
	}
}

func TestRunnerDiscoversMultipleNestedSuites(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n")
	writeTestFile(t, filepath.Join(root, "frontend", "package.json"), `{"scripts":{"test":"vitest"}}`)
	writeTestFile(t, filepath.Join(root, "api", "composer.json"), `{"require-dev":{"phpunit/phpunit":"^11"}}`)

	state := NewTestRunner(root).State()
	if len(state.Suites) != 3 {
		t.Fatalf("suites = %#v", state.Suites)
	}
	frameworks := []string{state.Suites[0].Framework, state.Suites[1].Framework, state.Suites[2].Framework}
	if strings.Join(frameworks, ",") != "Go,PHPUnit,JavaScript / TypeScript" {
		t.Fatalf("frameworks = %v", frameworks)
	}
}

func TestJavaScriptFrameworkRequiresTestScript(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"scripts":{"check":"svelte-check"}}`)
	if (javaScriptTestFramework{}).detect(root) {
		t.Fatal("project without test script was detected")
	}
	writeTestFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest run"}}`)
	if !(javaScriptTestFramework{}).detect(root) {
		t.Fatal("project with test script was not detected")
	}
}

func TestRunnerRefreshesSuitesWithoutLosingResults(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n")
	runner := NewTestRunner(root)
	goID := runner.State().Suites[0].ID

	runner.mu.Lock()
	runner.suites[goID].suite.Status = "passed"
	runner.suites[goID].suite.Results = []TestResult{{Name: "example.com/root", Status: "pass"}}
	runner.mu.Unlock()

	writeTestFile(t, filepath.Join(root, "frontend", "package.json"), `{"scripts":{"test":"vitest run"}}`)
	runner.Refresh()
	state := runner.State()
	if len(state.Suites) != 2 {
		t.Fatalf("suites after addition = %#v", state.Suites)
	}
	if state.Suites[0].Status != "passed" || len(state.Suites[0].Results) != 1 {
		t.Fatalf("existing result was lost: %#v", state.Suites[0])
	}

	if err := os.Remove(filepath.Join(root, "frontend", "package.json")); err != nil {
		t.Fatal(err)
	}
	runner.Refresh()
	if suites := runner.State().Suites; len(suites) != 1 {
		t.Fatalf("suites after removal = %#v", suites)
	}
}

func TestRunnerMarksCompletedSuiteStaleAfterRelevantChange(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n")
	source := filepath.Join(root, "service.go")
	writeTestFile(t, source, "package root\n")
	runner := NewTestRunner(root)
	suiteID := runner.State().Suites[0].ID
	started := time.Now()

	runner.mu.Lock()
	runtime := runner.suites[suiteID]
	runtime.suite.Status = "passed"
	runtime.suite.StartedAt = &started
	runtime.baseline = relevantFileSignatures(root, runtime.framework)
	runner.mu.Unlock()

	writeTestFile(t, source, "package root\n\nfunc Run() {}\n")
	runner.Refresh()
	suite := runner.State().Suites[0]
	if !suite.Stale || suite.Changed != 1 {
		t.Fatalf("suite was not marked stale: %#v", suite)
	}
}

func TestFrameworkFingerprintsIgnoreUnrelatedLanguages(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\n")
	writeTestFile(t, filepath.Join(root, "frontend", "app.ts"), "export const ready = true;\n")

	before := relevantFileSignatures(root, goTestFramework{})
	writeTestFile(t, filepath.Join(root, "frontend", "app.ts"), "export const ready = false;\n")
	after := relevantFileSignatures(root, goTestFramework{})
	if changed := changedFileCount(before, after); changed != 0 {
		t.Fatalf("unrelated files changed Go fingerprint by %d", changed)
	}
}

func TestRunnerPersistsAndReloadsTestHistory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/history\n\ngo 1.26\n")
	writeTestFile(t, filepath.Join(root, "history_test.go"), "package history\n\nimport \"testing\"\n\nfunc TestHistory(t *testing.T) {}\n")
	runner := NewTestRunner(root)
	if err := runner.Start(""); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for runner.State().Status == "running" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	state := runner.State()
	if state.Status != "passed" || len(state.Suites[0].History) != 1 {
		t.Fatalf("completed state = %#v", state)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "titancode", "test-history.json")); err != nil {
		t.Fatal(err)
	}

	reloaded := NewTestRunner(root).State()
	if len(reloaded.Suites) != 1 || len(reloaded.Suites[0].History) != 1 {
		t.Fatalf("reloaded state = %#v", reloaded)
	}
	if reloaded.Suites[0].History[0].Status != "passed" {
		t.Fatalf("reloaded run = %#v", reloaded.Suites[0].History[0])
	}
}

func TestRunnerLimitsLoadedHistory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "titancode"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/history\n")
	runs := make([]TestRun, maxTestRuns+5)
	for index := range runs {
		runs[index] = TestRun{Status: "passed", StartedAt: time.Unix(int64(index), 0)}
	}
	payload, err := json.Marshal(map[string][]TestRun{"go:.": runs})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, ".git", "titancode", "test-history.json"), string(payload))

	state := NewTestRunner(root).State()
	if got := len(state.Suites[0].History); got != maxTestRuns {
		t.Fatalf("history length = %d, want %d", got, maxTestRuns)
	}
}

func TestTestRunIsSlowerThanRecentSuccessfulRuns(t *testing.T) {
	history := []TestRun{
		{Status: "passed", DurationMS: 1000},
		{Status: "failed", DurationMS: 9000},
		{Status: "passed", DurationMS: 1200},
	}
	if !testRunIsSlower(1500, history) {
		t.Fatal("expected significant slowdown")
	}
	if testRunIsSlower(1250, history) {
		t.Fatal("minor runtime variation was marked as slow")
	}
}

func TestPHPUnitFrameworkParsesSummary(t *testing.T) {
	output := []byte("Tests: 5, Assertions: 5, Failures: 1.\n")
	results, _ := (phpUnitFramework{}).parse(output)
	if len(results) != 2 || results[0].Status != "pass" || results[1].Status != "fail" {
		t.Fatalf("results = %#v", results)
	}
}

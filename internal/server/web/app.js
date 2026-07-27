const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];
const number = new Intl.NumberFormat("de-DE");
const compact = new Intl.NumberFormat("de-DE", { notation: "compact", maximumFractionDigits: 1 });

const state = {
  snapshot: null,
  view: location.pathname === "/changes" ? "changes" : location.pathname === "/tests" ? "tests" : "overview",
  selectedPath: new URLSearchParams(location.search).get("file"),
  mode: "working",
  filter: "all",
  selectedTestSuite: null,
  selectedTestRun: null,
};

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function render(snapshot) {
  state.snapshot = snapshot;
  renderProject(snapshot);
  renderOverview(snapshot);
  renderReview(snapshot);
}

function renderProject({ project }) {
  $("#project-name").textContent = project.name;
  $("#project-path").textContent = project.path;
  $("#branch").textContent = project.isGit ? project.branch : "kein Git";
}

function renderOverview(snapshot) {
  const { summary, scannedAt } = snapshot;
  const modules = snapshot.modules ?? [];
  const changes = snapshot.changes ?? [];
  const languages = snapshot.languages ?? [];
  $("#metric-files").textContent = number.format(summary.sourceFiles);
  $("#metric-all-files").textContent = `${number.format(summary.files)} Dateien insgesamt`;
  $("#metric-lines").textContent = compact.format(summary.codeLines);
  $("#metric-modules").textContent = number.format(summary.modules);
  $("#metric-changes").textContent = number.format(summary.changes);
  $("#metric-diff").textContent = summary.changes
    ? `+${number.format(summary.insertions)} −${number.format(summary.deletions)} im Working Tree`
    : "Working Tree ist sauber";
  $("#change-count").textContent = summary.changes;
  $("#updated-at").textContent = `Aktualisiert ${relativeTime(new Date(scannedAt))}`;
  $("#scan-time").textContent = `Scan in ${summary.lastScanMs} ms · ${relativeTime(new Date(scannedAt))}`;
  $("#summary-copy").textContent = summary.changes === 0
    ? "Der Working Tree ist sauber. Alle erkannten Bereiche sind synchronisiert."
    : `${summary.changes} ${summary.changes === 1 ? "Änderung wartet" : "Änderungen warten"} auf deine Prüfung.`;

  $("#modules").classList.remove("skeleton-list");
  $("#modules").innerHTML = modules.length
    ? modules.map(module => `
      <div class="module-row">
        <div class="module-main">
          <i class="status-dot"></i>
          <span class="folder" aria-hidden="true">
            <svg viewBox="0 0 24 20" fill="none">
              <path d="M2.5 4.5h7l2-2h4.25c1.1 0 2 .9 2 2v1.25h1.75c1.1 0 2 .9 2 2v8.75c0 1.1-.9 2-2 2h-17c-1.1 0-2-.9-2-2v-10c0-1.1.9-2 2-2Z"/>
              <path d="M1 6h18.5c1.1 0 2 .9 2 2"/>
            </svg>
          </span>
          <div class="module-name"><strong>${escapeHTML(module.name)}</strong><small>${escapeHTML(module.path)}</small></div>
        </div>
        <div class="datum"><span>${number.format(module.files)}</span><small>Dateien</small></div>
        <div class="datum"><span>${compact.format(module.codeLines)}</span><small>Codezeilen</small></div>
        <div class="datum"><span>${escapeHTML(module.description)}</span><small>Bereich</small></div>
      </div>`).join("")
    : `<div class="empty-state">Noch keine Quelldateien erkannt</div>`;

  $("#changes").innerHTML = changes.length
    ? changes.slice(0, 12).map(change => `
      <div class="change-row" data-review-path="${escapeHTML(change.path)}">
        <div class="change-file"><strong>${escapeHTML(change.path)}</strong><small>${escapeHTML(statusLabel(change.status))}</small></div>
        <div class="diff"><span class="add">+${change.insertions}</span><span class="delete">−${change.deletions}</span></div>
        <span class="review-hint">Diff prüfen →</span>
      </div>`).join("")
    : `<div class="empty-state">✓ Keine uncommitteten Änderungen</div>`;
  $$("[data-review-path]").forEach(element => {
    element.addEventListener("click", () => openReview(element.dataset.reviewPath));
  });

  const findings = buildFindings(snapshot);
  $("#attention-count").textContent = findings.length;
  $("#attention").innerHTML = findings.length
    ? findings.map(item => `
      <div class="attention-item"><span>●</span><div><strong>${escapeHTML(item.title)}</strong><small>${escapeHTML(item.detail)}</small></div></div>`).join("")
    : `<div class="empty-state">✓ Keine unmittelbaren Befunde</div>`;

  $("#language-bar").innerHTML = languages
    .map(language => `<span style="width:${language.percentage}%;background:${language.color}" title="${escapeHTML(language.name)}"></span>`)
    .join("");
  $("#languages").innerHTML = languages.slice(0, 6)
    .map(language => `
      <div class="language-row"><i style="background:${language.color}"></i><span>${escapeHTML(language.name)}</span><span>${language.percentage.toFixed(1)}%</span></div>`)
    .join("") || `<div class="empty-state">Keine Sprachen erkannt</div>`;
}

function renderReview(snapshot) {
  const allChanges = snapshot.changes ?? [];
  const changes = allChanges.filter(change =>
    state.filter === "all" ||
    (state.filter === "working" && change.unstaged) ||
    (state.filter === "staged" && change.staged)
  );
  $("#review-count").textContent = allChanges.length;
  $("#review-summary").textContent = allChanges.length
    ? `${allChanges.length} ${allChanges.length === 1 ? "Datei wurde" : "Dateien wurden"} im Working Tree verändert.`
    : "Der Working Tree ist sauber.";

  if (state.selectedPath && !allChanges.some(change => change.path === state.selectedPath)) {
    state.selectedPath = null;
    clearDiff();
  }

  $("#review-files-list").innerHTML = changes.length
    ? changes.map(change => `
      <button class="review-file ${change.path === state.selectedPath ? "active" : ""}" type="button" data-file-path="${escapeHTML(change.path)}">
        <span class="file-status">${statusCode(change.status)}</span>
        <span class="review-file-main">
          <strong>${escapeHTML(fileName(change.path))}</strong>
          <small>${escapeHTML(directoryName(change.path))}</small>
          <span class="stage-badges">
            ${change.unstaged ? `<span class="stage-badge">Working</span>` : ""}
            ${change.staged ? `<span class="stage-badge staged">Staged</span>` : ""}
          </span>
        </span>
        <span class="review-file-diff"><span class="add">+${change.insertions}</span> <span class="delete">−${change.deletions}</span></span>
      </button>`).join("")
    : `<div class="empty-state">${allChanges.length ? "Keine Dateien in diesem Bereich" : "✓ Keine Änderungen"}</div>`;

  $$("[data-file-path]").forEach(element => {
    element.addEventListener("click", () => selectFile(element.dataset.filePath));
  });
  updateDiffToolbar();
}

function selectFile(path) {
  state.selectedPath = path;
  const change = currentChange();
  state.mode = change?.unstaged ? "working" : "staged";
  history.replaceState({}, "", `/changes?file=${encodeURIComponent(path)}`);
  renderReview(state.snapshot);
  loadDiff();
}

function openReview(path = null) {
  state.view = "changes";
  if (path) {
    state.selectedPath = path;
    const change = currentChange();
    state.mode = change?.unstaged ? "working" : "staged";
  }
  const query = state.selectedPath ? `?file=${encodeURIComponent(state.selectedPath)}` : "";
  history.pushState({}, "", `/changes${query}`);
  applyRoute();
  renderReview(state.snapshot);
  if (state.selectedPath) loadDiff();
}

function navigateOverview() {
  navigate("overview");
}

function navigate(view) {
  state.view = view;
  history.pushState({}, "", view === "overview" ? "/" : `/${view}`);
  applyRoute();
  if (view === "tests") fetchTestState();
}

function applyRoute() {
  $("#overview-view").classList.toggle("view-hidden", state.view !== "overview");
  $("#changes-view").classList.toggle("view-hidden", state.view !== "changes");
  $("#tests-view").classList.toggle("view-hidden", state.view !== "tests");
  $$("nav [data-route]").forEach(link => link.classList.toggle("active", link.dataset.route === state.view));
}

async function loadDiff() {
  const change = currentChange();
  if (!change) {
    clearDiff();
    return;
  }
  $("#diff-content").className = "diff-content empty-diff";
  $("#diff-content").innerHTML = `<div class="diff-placeholder"><span class="pulse"></span><strong>Diff wird geladen …</strong></div>`;
  updateDiffToolbar();
  try {
    const query = new URLSearchParams({ path: change.path, mode: state.mode });
    const response = await fetch(`/api/diff?${query}`);
    const payload = await response.json();
    if (!response.ok) throw new Error(payload.error || "Diff konnte nicht geladen werden");
    renderDiff(payload);
  } catch (error) {
    $("#diff-content").className = "diff-content empty-diff";
    $("#diff-content").innerHTML = `<div class="diff-placeholder"><strong>Diff nicht verfügbar</strong><small>${escapeHTML(error.message)}</small></div>`;
  }
}

function renderDiff(diff) {
  $("#diff-content").className = "diff-content";
  if (diff.binary) {
    $("#diff-content").innerHTML = `<div class="diff-notice">Binärdateien können nicht als Text-Diff dargestellt werden.</div>`;
  } else if (!diff.content) {
    $("#diff-content").className = "diff-content empty-diff";
    $("#diff-content").innerHTML = `<div class="diff-placeholder"><span>✓</span><strong>Keine Änderungen in diesem Bereich</strong><small>Wechsle zwischen Working Tree und Staged.</small></div>`;
  } else {
    $("#diff-content").innerHTML = `<div class="diff-code">${formatUnifiedDiff(diff.content)}</div>${diff.truncated ? `<div class="diff-notice">Diff wurde nach 2 MiB gekürzt.</div>` : ""}`;
  }
  $("#diff-meta").innerHTML = `
    <span>${state.mode === "working" ? "Nicht gestagte Änderungen" : "Gestagte Änderungen"}</span>
    <span>${diff.binary ? "Binärdatei" : "Unified Diff · 3 Kontextzeilen"}</span>
    ${diff.truncated ? "<span>Ausgabe gekürzt</span>" : ""}`;
}

function formatUnifiedDiff(content) {
  let oldLine = null;
  let newLine = null;
  return content.split("\n").map(line => {
    const hunk = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
    if (hunk) {
      oldLine = Number(hunk[1]);
      newLine = Number(hunk[2]);
      return diffLine(line, "", "", "", "hunk");
    }
    if (line.startsWith("diff --git") || line.startsWith("index ") || line.startsWith("--- ") || line.startsWith("+++ ")) {
      return diffLine(line, "", "", "", "header");
    }
    if (oldLine === null || newLine === null) return diffLine(line, "", "", "", "header");
    if (line.startsWith("+")) {
      const result = diffLine(line.slice(1), "", newLine, "+", "added");
      newLine++;
      return result;
    }
    if (line.startsWith("-")) {
      const result = diffLine(line.slice(1), oldLine, "", "−", "removed");
      oldLine++;
      return result;
    }
    const result = diffLine(line.startsWith(" ") ? line.slice(1) : line, oldLine, newLine, " ", "");
    oldLine++;
    newLine++;
    return result;
  }).join("");
}

function diffLine(text, oldNumber, newNumber, prefix, type) {
  return `<div class="diff-line ${type}">
    <span class="diff-number">${oldNumber}</span><span class="diff-number">${newNumber}</span>
    <span class="diff-prefix">${prefix}</span><span class="diff-text">${escapeHTML(text)}</span>
  </div>`;
}

async function runGitAction() {
  const change = currentChange();
  if (!change) return;
  const action = state.mode === "staged" ? "unstage" : "stage";
  const button = $("#git-action");
  button.disabled = true;
  try {
    const response = await fetch(`/api/git/${action}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path: change.path }),
    });
    const payload = await response.json();
    if (!response.ok) throw new Error(payload.error || "Git-Aktion fehlgeschlagen");
    showToast(action === "stage" ? "Datei wurde gestagt." : "Datei wurde aus der Staging Area entfernt.");
    state.mode = action === "stage" ? "staged" : "working";
    await fetchSnapshot();
    await loadDiff();
  } catch (error) {
    showToast(error.message, true);
  } finally {
    updateDiffToolbar();
  }
}

function updateDiffToolbar() {
  const change = currentChange();
  $("#diff-path").textContent = change?.path ?? "Keine Datei ausgewählt";
  $("#diff-status").textContent = change ? statusLabel(change.status) : "";
  $$("[data-mode]").forEach(button => button.classList.toggle("active", button.dataset.mode === state.mode));
  const action = $("#git-action");
  action.textContent = state.mode === "staged" ? "Unstagen" : "Stagen";
  action.disabled = !change || (state.mode === "working" ? !change.unstaged : !change.staged);
}

function clearDiff() {
  $("#diff-content").className = "diff-content empty-diff";
  $("#diff-content").innerHTML = `<div class="diff-placeholder"><span>↯</span><strong>Noch kein Diff ausgewählt</strong><small>Wähle eine Datei, um ihre Änderungen zu prüfen.</small></div>`;
  $("#diff-meta").innerHTML = "<span>Wähle links eine geänderte Datei aus.</span>";
  updateDiffToolbar();
}

function currentChange() {
  return (state.snapshot?.changes ?? []).find(change => change.path === state.selectedPath);
}

function buildFindings(snapshot) {
  const items = [];
  if (!snapshot.project.isGit) items.push({ title: "Keine Git-Historie", detail: "Regressionen benötigen eine Git-Baseline." });
  if (snapshot.summary.changes > 8) items.push({ title: "Große Arbeitsmenge", detail: `${snapshot.summary.changes} Dateien wurden gleichzeitig verändert.` });
  if (snapshot.summary.insertions + snapshot.summary.deletions > 500) {
    items.push({ title: "Umfangreicher Diff", detail: `${snapshot.summary.insertions + snapshot.summary.deletions} geänderte Zeilen prüfen.` });
  }
  const untracked = (snapshot.changes ?? []).filter(change => change.status === "untracked").length;
  if (untracked) items.push({ title: "Neue Dateien", detail: `${untracked} ${untracked === 1 ? "Datei ist" : "Dateien sind"} noch nicht versioniert.` });
  return items;
}

function statusLabel(status) {
  return ({ modified: "Geändert", added: "Hinzugefügt", deleted: "Gelöscht", renamed: "Umbenannt", untracked: "Nicht versioniert" })[status] || status;
}

function statusCode(status) {
  return ({ modified: "M", added: "A", deleted: "D", renamed: "R", untracked: "U" })[status] || "?";
}

function fileName(path) {
  return path.split("/").at(-1);
}

function directoryName(path) {
  const parts = path.split("/");
  return parts.length > 1 ? parts.slice(0, -1).join("/") : "Repository root";
}

function relativeTime(date) {
  const seconds = Math.max(0, Math.round((Date.now() - date.getTime()) / 1000));
  if (seconds < 5) return "gerade eben";
  if (seconds < 60) return `vor ${seconds} Sek.`;
  return `vor ${Math.floor(seconds / 60)} Min.`;
}

function showToast(message, error = false) {
  $(".toast")?.remove();
  const toast = document.createElement("div");
  toast.className = `toast${error ? " error" : ""}`;
  toast.textContent = message;
  document.body.append(toast);
  setTimeout(() => toast.remove(), 3500);
}

async function fetchSnapshot() {
  const response = await fetch("/api/snapshot");
  if (!response.ok) throw new Error("Snapshot nicht verfügbar");
  render(await response.json());
}

async function fetchTestState() {
  try {
    const response = await fetch("/api/tests");
    const testState = await response.json();
    if (!response.ok) throw new Error(testState.error || "Teststatus nicht verfügbar");
    renderTestState(testState);
  } catch (error) {
    $("#test-summary").textContent = error.message;
    $("#run-tests").disabled = true;
  }
}

function renderTestState(testState) {
  const suites = testState.suites ?? [];
  if (!state.selectedTestSuite || !suites.some(suite => suite.id === state.selectedTestSuite)) {
    state.selectedTestSuite = suites[0]?.id ?? null;
  }
  const selected = suites.find(suite => suite.id === state.selectedTestSuite);
  if (state.selectedTestRun && !selected?.history?.some(run => run.startedAt === state.selectedTestRun)) {
    state.selectedTestRun = null;
  }
  const results = suites.flatMap(suite => suite.results ?? []);
  const passed = results.filter(item => item.status === "pass").length;
  const failed = results.filter(item => item.status === "fail").length;
  const stale = suites.filter(suite => suite.stale).length;
  const running = testState.status === "running";
  const statusLabels = {
    idle: "Bereit", running: "Läuft", passed: "Erfolgreich", failed: "Fehlgeschlagen",
    canceled: "Abgebrochen", timeout: "Zeitüberschreitung",
  };
  $("#test-status").className = `test-status ${testState.status}`;
  $("#test-status").textContent = statusLabels[testState.status] ?? testState.status;
  $("#test-command").textContent = suites.length
    ? `${suites.length} ${suites.length === 1 ? "Suite wurde" : "Suites wurden"} im Repository erkannt`
    : "Keine ausführbare Testkonfiguration gefunden";
  $("#test-summary").textContent = !suites.length
    ? "Für dieses Repository wurde noch keine unterstützte Test-Suite erkannt."
    : running
      ? "Mindestens eine Test-Suite wird ausgeführt …"
      : stale
        ? `${stale} ${stale === 1 ? "Testergebnis ist" : "Testergebnisse sind"} nach Codeänderungen veraltet.`
        : `${suites.length} ${suites.length === 1 ? "Suite ist" : "Suites sind"} bereit.`;
  $("#test-packages").textContent = results.length ? number.format(results.length) : "—";
  $("#test-passed").textContent = results.length ? number.format(passed) : "—";
  $("#test-failed").textContent = results.length ? number.format(failed) : "—";
  const duration = Math.max(0, ...suites.map(suite => suite.durationMs ?? 0));
  $("#test-duration").textContent = duration ? formatDuration(duration) : "—";
  $("#run-tests").disabled = !suites.length || running;
  $("#run-tests").textContent = suites.some(suite => suite.finishedAt) ? "Alle erneut ausführen" : "Alle Tests starten";
  $("#cancel-tests").classList.toggle("view-hidden", !running);
  $("#test-suites-list").innerHTML = suites.length
    ? suites.map(suite => `
      <button class="test-suite ${suite.id === state.selectedTestSuite ? "active" : ""} ${suite.stale ? "stale" : ""}" type="button" data-suite-id="${escapeHTML(suite.id)}">
        <span class="test-result-icon ${suite.stale ? "skip" : suiteStatusIcon(suite.status)}">${suite.stale ? "!" : suiteStatusSymbol(suite.status)}</span>
        <span class="test-suite-main">
          <strong>${escapeHTML(suite.name)}</strong>
          <small>${escapeHTML(suite.path)} · ${escapeHTML(suite.framework)}</small>
          <code>${escapeHTML(suite.command)}</code>
        </span>
        <span class="test-suite-state">${suite.stale ? `Veraltet · ${suite.changedFiles} ${suite.changedFiles === 1 ? "Datei" : "Dateien"}` : statusLabels[suite.status] ?? suite.status}</span>
        <span class="suite-run" role="button" tabindex="0" data-run-suite="${escapeHTML(suite.id)}">${suite.status === "running" ? "Läuft …" : suite.stale ? "Erneut" : "Starten"}</span>
      </button>`).join("")
    : `<div class="empty-state">Keine unterstützten Test-Suites erkannt</div>`;
  $$("[data-suite-id]").forEach(element => {
    element.addEventListener("click", () => {
      state.selectedTestSuite = element.dataset.suiteId;
      state.selectedTestRun = null;
      renderTestState(testState);
    });
  });
  $$("[data-run-suite]").forEach(element => {
    element.addEventListener("click", event => {
      event.stopPropagation();
      testAction("run", element.dataset.runSuite);
    });
  });
  const selectedResults = selected?.results ?? [];
  $("#test-packages-list").innerHTML = selectedResults.length
    ? selectedResults.map(item => `
      <div class="test-package">
        <span class="test-result-icon ${item.status}">${item.status === "pass" ? "✓" : item.status === "skip" ? "−" : "×"}</span>
        <strong>${escapeHTML(item.name)}</strong>
        <span>${formatDuration(item.durationMs)}</span>
      </div>`).join("")
    : `<div class="empty-state">${selected?.status === "running" ? "Testergebnisse werden gesammelt …" : "Noch keine Ergebnisse für diese Suite"}</div>`;
  const history = selected?.history ?? [];
  $("#test-history-list").innerHTML = history.length
    ? history.map(run => `
      <button class="test-history-row ${run.startedAt === state.selectedTestRun ? "active" : ""}" type="button" data-test-run="${escapeHTML(run.startedAt)}">
        <span class="test-result-icon ${suiteStatusIcon(run.status)}">${suiteStatusSymbol(run.status)}</span>
        <span><strong>${escapeHTML(statusLabels[run.status] ?? run.status)}${run.slow ? " · Langsamer" : ""}</strong><small>${new Date(run.finishedAt).toLocaleString("de-DE")}</small></span>
        <span>${number.format(run.results?.length ?? 0)} Ergebnisse</span>
        <span>${formatDuration(run.durationMs)}</span>
      </button>`).join("")
    : `<div class="empty-state">Noch keine gespeicherten Testläufe</div>`;
  $$("[data-test-run]").forEach(element => {
    element.addEventListener("click", () => {
      state.selectedTestRun = element.dataset.testRun;
      renderTestState(testState);
    });
  });
  const selectedRun = history.find(run => run.startedAt === state.selectedTestRun);
  $("#test-output").textContent = selectedRun
    ? selectedRun.output || selectedRun.error || "Für diesen erfolgreichen Lauf wurde keine Ausgabe gespeichert."
    : selected?.output || selected?.error || "Noch keine Ausgabe für diese Suite vorhanden.";
}

function suiteStatusIcon(status) {
  if (status === "passed") return "pass";
  if (status === "failed" || status === "timeout") return "fail";
  if (status === "canceled") return "skip";
  return "";
}

function suiteStatusSymbol(status) {
  if (status === "passed") return "✓";
  if (status === "failed" || status === "timeout") return "×";
  if (status === "running") return "…";
  if (status === "canceled") return "−";
  return "○";
}

async function testAction(action, suiteId = "") {
  const button = action === "run" ? $("#run-tests") : $("#cancel-tests");
  button.disabled = true;
  try {
    const response = await fetch(`/api/tests/${action}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ suiteId }),
    });
    const payload = await response.json();
    if (!response.ok) throw new Error(payload.error || "Testaktion fehlgeschlagen");
    renderTestState(payload);
  } catch (error) {
    showToast(error.message, true);
  } finally {
    fetchTestState();
  }
}

function formatDuration(milliseconds) {
  if (milliseconds < 1000) return `${milliseconds} ms`;
  return `${(milliseconds / 1000).toFixed(1)} s`;
}

$$("nav [data-route]").forEach(link => {
  link.addEventListener("click", event => {
    event.preventDefault();
    link.dataset.route === "changes" ? openReview() : navigate(link.dataset.route);
  });
});
$("[data-open-review]").addEventListener("click", () => openReview());
$("[data-route-button='overview']").addEventListener("click", navigateOverview);
$$("[data-filter]").forEach(button => {
  button.addEventListener("click", () => {
    state.filter = button.dataset.filter;
    $$("[data-filter]").forEach(candidate => candidate.classList.toggle("active", candidate === button));
    renderReview(state.snapshot);
  });
});
$$("[data-mode]").forEach(button => {
  button.addEventListener("click", () => {
    state.mode = button.dataset.mode;
    updateDiffToolbar();
    loadDiff();
  });
});
$("#git-action").addEventListener("click", runGitAction);
$("#run-tests").addEventListener("click", () => testAction("run"));
$("#cancel-tests").addEventListener("click", () => testAction("cancel"));
window.addEventListener("popstate", () => {
  state.view = location.pathname === "/changes" ? "changes" : location.pathname === "/tests" ? "tests" : "overview";
  state.selectedPath = new URLSearchParams(location.search).get("file");
  applyRoute();
  if (state.view === "changes" && state.selectedPath) loadDiff();
  if (state.view === "tests") fetchTestState();
});

const events = new EventSource("/api/events");
events.addEventListener("snapshot", event => render(JSON.parse(event.data)));
setInterval(() => {
  if (state.view === "tests") fetchTestState();
}, 1000);

applyRoute();
fetchSnapshot()
  .then(() => {
    if (state.view === "changes" && state.selectedPath) selectFile(state.selectedPath);
    if (state.view === "tests") fetchTestState();
  })
  .catch(error => {
    $("#summary-copy").textContent = `Verbindung fehlgeschlagen: ${error.message}`;
  });

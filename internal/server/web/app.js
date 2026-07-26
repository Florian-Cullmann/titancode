const $ = (selector) => document.querySelector(selector);
const number = new Intl.NumberFormat("de-DE");
const compact = new Intl.NumberFormat("de-DE", { notation: "compact", maximumFractionDigits: 1 });

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function render(snapshot) {
  const { project, summary, modules, changes, languages, scannedAt } = snapshot;
  $("#project-name").textContent = project.name;
  $("#project-path").textContent = project.path;
  $("#branch").textContent = project.isGit ? project.branch : "kein Git";
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

  if (summary.changes === 0) {
    $("#summary-copy").textContent = "Der Working Tree ist sauber. Alle erkannten Bereiche sind synchronisiert.";
  } else {
    $("#summary-copy").textContent = `${summary.changes} ${summary.changes === 1 ? "Änderung wartet" : "Änderungen warten"} auf deine Prüfung.`;
  }

  $("#modules").classList.remove("skeleton-list");
  $("#modules").innerHTML = modules.length
    ? modules.map(module => `
      <div class="module-row">
        <div class="module-main">
          <i class="status-dot"></i><span class="folder">⌑</span>
          <div class="module-name"><strong>${escapeHTML(module.name)}</strong><small>${escapeHTML(module.path)}</small></div>
        </div>
        <div class="datum"><span>${number.format(module.files)}</span><small>Dateien</small></div>
        <div class="datum"><span>${compact.format(module.codeLines)}</span><small>Codezeilen</small></div>
        <div class="datum"><span>${escapeHTML(module.description)}</span><small>Bereich</small></div>
      </div>`).join("")
    : `<div class="empty-state">Noch keine Quelldateien erkannt</div>`;

  $("#changes").innerHTML = changes.length
    ? changes.map(change => `
      <div class="change-row">
        <div class="change-file"><strong>${escapeHTML(change.path)}</strong><small>${escapeHTML(statusLabel(change.status))}</small></div>
        <div class="diff"><span class="add">+${change.insertions}</span><span class="delete">−${change.deletions}</span></div>
        <span class="review-hint">Diff prüfen →</span>
      </div>`).join("")
    : `<div class="empty-state">✓ Keine uncommitteten Änderungen</div>`;

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

function buildFindings(snapshot) {
  const items = [];
  if (!snapshot.project.isGit) {
    items.push({ title: "Keine Git-Historie", detail: "Regressionen benötigen eine Git-Baseline." });
  }
  if (snapshot.summary.changes > 8) {
    items.push({ title: "Große Arbeitsmenge", detail: `${snapshot.summary.changes} Dateien wurden gleichzeitig verändert.` });
  }
  if (snapshot.summary.insertions + snapshot.summary.deletions > 500) {
    items.push({ title: "Umfangreicher Diff", detail: `${snapshot.summary.insertions + snapshot.summary.deletions} geänderte Zeilen prüfen.` });
  }
  const untracked = snapshot.changes.filter(change => change.status === "untracked").length;
  if (untracked) {
    items.push({ title: "Neue Dateien", detail: `${untracked} ${untracked === 1 ? "Datei ist" : "Dateien sind"} noch nicht versioniert.` });
  }
  return items;
}

function statusLabel(status) {
  return ({ modified: "Geändert", added: "Hinzugefügt", deleted: "Gelöscht", renamed: "Umbenannt", untracked: "Nicht versioniert" })[status] || status;
}

function relativeTime(date) {
  const seconds = Math.max(0, Math.round((Date.now() - date.getTime()) / 1000));
  if (seconds < 5) return "gerade eben";
  if (seconds < 60) return `vor ${seconds} Sek.`;
  return `vor ${Math.floor(seconds / 60)} Min.`;
}

async function initialLoad() {
  try {
    const response = await fetch("/api/snapshot");
    if (!response.ok) throw new Error("Snapshot nicht verfügbar");
    render(await response.json());
  } catch (error) {
    $("#summary-copy").textContent = `Verbindung fehlgeschlagen: ${error.message}`;
  }
}

const events = new EventSource("/api/events");
events.addEventListener("snapshot", event => render(JSON.parse(event.data)));
events.onerror = () => { $(".live").lastChild.textContent = " Verbinde …"; };
events.onopen = () => { $(".live").lastChild.textContent = " Live"; };
initialLoad();


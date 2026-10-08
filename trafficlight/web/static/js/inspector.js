// @ts-check
import { badgeHtml } from "./badge.js";
import { CODE_LIMIT, DETAIL_CACHE_SIZE, JSON_SYNTAX, MAX_TABS, TABS, TEXT_SYNTAX, TIME_ZONE } from "./constants.js";
import { renderDiff } from "./diff.js";
import { highlightCode } from "./highlight.js";
import { prefs, savePrefs } from "./prefs.js";
import { highlightTerms, matches } from "./query.js";
import { state } from "./state.js";
import { jsonToNode, makeMatcher, renderTree, setNodeOpenDeep } from "./tree.js";
import { toast, ui } from "./ui.js";
import { escapeHtml, formatBytes, formatDate, formatDelta } from "./util.js";
/** @typedef {import("./types.js").Row} Row */
/** @typedef {import("./types.js").Tab} Tab */

export const detailCache = new Map();
let nextTabId = 1;

// ------------------------------------------------------------------ the open tabs

const tabs = () => state.panes.tabs;
export const activeTab = () => tabs().find((tab) => tab.id === state.panes.activeId) ?? null;
export const splitTab = () => (state.panes.splitId === null ? null : tabs().find((t) => t.id === state.panes.splitId) ?? null);

/** The pane the toolbar acts on: the last one clicked, or the active one */
export function focusedTab() {
  const focused = tabs().find((tab) => tab.id === state.panes.focusId);
  return focused && isVisible(focused) ? focused : activeTab();
}

const isVisible = (tab) => tab.id === state.panes.activeId || tab.id === state.panes.splitId;

/** @returns {Tab} */
function makeTab(row, ephemeral) {
  return {
    id: nextTabId++,
    key: row.key,
    row,
    detail: null,
    error: null,
    request: null,
    sectionState: new Map(),
    renderedDetail: null,
    scrollTop: 0,
    ephemeral,
    view: prefs.tab,
    find: { query: "", caseSensitive: false, regex: false, matchesOnly: false, index: 0 },
    el: null,
  };
}

/**
 * Opens a proto in the inspector.
 * - preview: clicking a row or walking the log with the arrow keys. Reuses the one ephemeral tab,
 *   so browsing cannot bury the log in tabs
 * - permanent: a double click or Enter, the tab stays until it is closed
 * - background: ctrl or middle click, opens without taking you away from what you are reading
 */
export function openInspect(row, mode = "preview") {
  if (!row) return;

  const existing = tabs().find((tab) => tab.key === row.key);
  if (existing) {
    if (mode !== "preview") existing.ephemeral = false;
    if (mode !== "background") activate(existing);
    else renderTabs();
    return;
  }

  if (mode === "preview") {
    // there is at most one ephemeral tab, wherever it sits
    const reusable = tabs().find((tab) => tab.ephemeral);
    if (reusable) {
      retarget(reusable, row);
      activate(reusable);
      return;
    }
  }

  if (tabs().length >= MAX_TABS) {
    toast(`That is ${MAX_TABS} tabs already, close one first`);
    return;
  }

  const tab = makeTab(row, mode === "preview");
  const after = tabs().findIndex((candidate) => candidate.id === state.panes.activeId);
  tabs().splice(after === -1 ? tabs().length : after + 1, 0, tab);

  if (mode === "background") {
    renderTabs();
    loadDetail(tab);
  } else {
    activate(tab);
  }
}

function retarget(tab, row) {
  tab.request?.abort();
  tab.request = null;
  tab.key = row.key;
  tab.row = row;
  tab.detail = null;
  tab.error = null;
  tab.renderedDetail = null;
  tab.sectionState = new Map();
  tab.scrollTop = 0;
  tab.find.index = 0;
}

function activate(tab) {
  state.panes.activeId = tab.id;
  state.panes.focusId = tab.id;
  if (state.panes.splitId === tab.id) {
    // it was the split pane, so the two swap rather than showing the same proto twice
    state.panes.splitId = tabs().find((t) => t.id !== tab.id && t.id === state.panes.activeId)?.id ?? null;
  }
  syncActive();
  renderTabs();
  renderPanes();
  loadDetail(tab);
}

/** state.inspected and state.detail follow the active tab, so the rest of the app keeps working */
function syncActive() {
  const tab = activeTab();
  state.inspected = tab?.row ?? null;
  state.detail = tab?.detail ?? null;
  // the find bar and the address bar belong to main.js, this is how they hear about it
  document.dispatchEvent(new CustomEvent("tabchange"));
}

export function closeTab(id) {
  const index = tabs().findIndex((tab) => tab.id === id);
  if (index === -1) return;

  const [closed] = tabs().splice(index, 1);
  closed.request?.abort();
  closed.el?.remove();
  if (state.panes.splitId === id) state.panes.splitId = null;
  if (state.panes.activeId === id) {
    const next = tabs()[index] ?? tabs()[index - 1] ?? null;
    state.panes.activeId = next?.id ?? null;
    state.panes.focusId = next?.id ?? null;
  }
  if (state.panes.focusId === id) state.panes.focusId = state.panes.activeId;

  syncActive();
  renderTabs();
  renderPanes();
  if (!tabs().length) showEmpty();
}

export function closeActiveTab() {
  if (state.panes.activeId !== null) closeTab(state.panes.activeId);
}

/** Steps through the tabs with [ and ] */
export function cycleTab(delta) {
  if (tabs().length < 2) return;
  const index = tabs().findIndex((tab) => tab.id === state.panes.activeId);
  activate(tabs()[(index + delta + tabs().length) % tabs().length]);
}

export function setSplit(on) {
  if (on && tabs().length < 2) {
    toast("Open a second proto to compare them");
    return;
  }
  prefs.split = on;
  savePrefs();
  if (on) {
    const index = tabs().findIndex((tab) => tab.id === state.panes.activeId);
    const partner = tabs()[index + 1] ?? tabs()[index - 1];
    state.panes.splitId = partner?.id ?? null;
  } else {
    state.panes.splitId = null;
  }
  renderTabs();
  renderPanes();
  const partner = splitTab();
  if (partner) loadDetail(partner);
}

/** Shows a tab in the second pane without leaving the one you are reading */
export function showInSplit(id) {
  if (id === state.panes.activeId) return;
  prefs.split = true;
  savePrefs();
  state.panes.splitId = id;
  renderTabs();
  renderPanes();
  const tab = tabs().find((candidate) => candidate.id === id);
  if (tab) loadDetail(tab);
}

export function clearInspector() {
  for (const tab of tabs()) {
    tab.request?.abort();
    tab.el?.remove();
  }
  state.panes = { tabs: [], activeId: null, splitId: null, focusId: null };
  syncActive();
  renderTabs();
  showEmpty();
}

function showEmpty() {
  ui.inspectorEmpty.hidden = false;
  ui.inspectorTools.hidden = true;
  ui.panes.replaceChildren();
}

/** After a reconnect the row objects are new, so every tab looks its own up again by key */
export function rebindTabs() {
  const byKey = new Map(state.rows.map((row) => [row.key, row]));
  for (const tab of tabs()) {
    const row = byKey.get(tab.key);
    if (row) {
      tab.row = row;
      tab.stale = false;
    } else {
      tab.stale = true;
    }
  }
  syncActive();
  renderTabs();
  for (const tab of tabs()) if (isVisible(tab)) renderPaneHead(tab);
}

// ------------------------------------------------------------------ loading a proto

async function loadDetail(tab) {
  renderPanes();
  if (tab.detail) {
    syncActive();
    renderPaneBody(tab);
    return;
  }

  const cached = detailCache.get(tab.key);
  if (cached) {
    tab.detail = cached;
    tab.error = null;
    syncActive();
    renderPaneBody(tab);
    return;
  }

  tab.request?.abort();
  const controller = new AbortController();
  tab.request = controller;
  tab.el?.querySelector(".pane-body")?.classList.add("loading");

  try {
    const response = await fetch(`api/records/${tab.row.record.id}/${tab.row.index}`, { signal: controller.signal });
    if (response.status === 401) {
      location.assign("login");
      return;
    }
    if (!response.ok) {
      throw new Error(
        response.status === 404
          ? "This proto isn't available anymore, it was cleared or dropped from the log."
          : `Couldn't load this proto (HTTP ${response.status}).`,
      );
    }
    const detail = await response.json();
    detailCache.set(tab.key, detail);
    if (detailCache.size > DETAIL_CACHE_SIZE) detailCache.delete(detailCache.keys().next().value);
    if (tab.request !== controller) return;
    tab.detail = detail;
    tab.error = null;
    syncActive();
    if (state.panes.diff) renderPanes();
    else renderPaneBody(tab);
  } catch (error) {
    // the tab moved on to another proto in the meantime
    if (tab.request !== controller) return;
    tab.error = error instanceof Error ? error.message : String(error);
    renderPaneBody(tab);
  } finally {
    if (tab.request === controller) {
      tab.request = null;
      tab.el?.querySelector(".pane-body")?.classList.remove("loading");
    }
  }
}

/** list.js calls this when a row is selected */
export function inspect(row) {
  openInspect(row, "preview");
}

// ------------------------------------------------------------------ the tab strip

export function renderTabs() {
  const open = tabs();
  ui.docTabs.hidden = open.length === 0;
  ui.splitBtn.hidden = open.length < 2;
  ui.splitBtn.setAttribute("aria-pressed", String(Boolean(prefs.split && state.panes.splitId !== null)));

  ui.docTabs.innerHTML = open
    .map((tab) => {
      const classes = [
        "doc-tab",
        tab.id === state.panes.activeId ? "active" : "",
        tab.id === state.panes.splitId ? "split" : "",
        tab.ephemeral ? "ephemeral" : "",
        tab.stale ? "stale" : "",
      ]
        .filter(Boolean)
        .join(" ");
      const title = tab.stale ? "This proto is no longer in the log" : `${tab.row.method} · #${tab.row.record.rpc_id}`;
      return (
        `<div class="${classes}" data-tab="${tab.id}" title="${escapeHtml(title)}">` +
        `<span class="doc-tab-name">${escapeHtml(tab.row.method)}</span>` +
        `<span class="doc-tab-id">#${tab.row.record.rpc_id}</span>` +
        '<button class="doc-tab-close" type="button" data-close aria-label="Close this tab (w)">' +
        '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="m4.5 4.5 7 7M11.5 4.5l-7 7"/></svg></button>' +
        "</div>"
      );
    })
    .join("");
}

// ------------------------------------------------------------------ the panes

export function renderPanes() {
  const visible = [activeTab(), splitTab()].filter(Boolean);
  if (!visible.length) {
    showEmpty();
    return;
  }

  ui.inspectorEmpty.hidden = true;
  ui.inspectorTools.hidden = false;
  ui.diffBtn.hidden = visible.length < 2;
  ui.diffBtn.setAttribute("aria-pressed", String(Boolean(state.panes.diff)));

  if (state.panes.diff && visible.length === 2) {
    renderDiffPane(visible[0], visible[1]);
    return;
  }
  ui.panes.classList.toggle("split", visible.length > 1);

  for (const tab of visible) {
    if (!tab.el) {
      const pane = document.createElement("div");
      pane.className = "pane";
      pane.dataset.tab = String(tab.id);
      pane.innerHTML = '<div class="pane-head"></div><div class="pane-note" hidden></div><div class="pane-body"></div>';
      tab.el = pane;
      renderPaneHead(tab);
      renderPaneBody(tab);
    }
    tab.el.classList.toggle("focused", visible.length > 1 && tab.id === focusedTab()?.id);
  }
  ui.panes.replaceChildren(...visible.map((tab) => /** @type {HTMLElement} */ (tab.el)));
  updateToolsForFocus();
}

/** @type {boolean} whether the unchanged lines are folded away */
let diffShowUnchanged = false;

export function setDiff(on) {
  if (on && (!activeTab() || !splitTab())) {
    toast("Split two protos first, then compare them");
    return;
  }
  state.panes.diff = on;
  renderPanes();
}

export function toggleDiffContext() {
  diffShowUnchanged = !diffShowUnchanged;
  renderPanes();
}

function renderDiffPane(left, right) {
  ui.panes.classList.remove("split");
  const pane = document.createElement("div");
  pane.className = "pane";

  if (!left.detail || !right.detail) {
    pane.innerHTML = '<div class="inspector-message">Loading both protos\u2026</div>';
    ui.panes.replaceChildren(pane);
    // whichever side is still missing is already being fetched, this redraws when it lands
    return;
  }

  const { element, changes } = renderDiff(left.detail, right.detail, diffShowUnchanged);
  const head = document.createElement("div");
  head.className = "pane-head diff-head";
  head.innerHTML =
    '<div class="ih-title">' +
    `<span class="diff-side del">\u2212 ${escapeHtml(left.row.method)} <span class="ih-value">#${left.row.record.rpc_id}</span></span>` +
    `<span class="diff-side add">+ ${escapeHtml(right.row.method)} <span class="ih-value">#${right.row.record.rpc_id}</span></span>` +
    '<span class="ih-actions">' +
    `<button class="mini${diffShowUnchanged ? " on" : ""}" type="button" data-action="diff-context">` +
    `${diffShowUnchanged ? "Hide unchanged" : "Show unchanged"}</button>` +
    "</span></div>" +
    `<div class="ih-meta"><span>${changes ? `${changes} changed lines` : "The two are identical"}</span>` +
    '<span class="sep">\u00b7</span><span>Compared as JSON</span></div>';

  const body = document.createElement("div");
  body.className = "pane-body";
  body.append(element);
  pane.append(head, body);
  ui.panes.replaceChildren(pane);
}

export function focusPane(id) {
  if (state.panes.focusId === id) return;
  state.panes.focusId = id;
  document.dispatchEvent(new CustomEvent("tabchange"));
  for (const tab of tabs()) tab.el?.classList.toggle("focused", state.panes.splitId !== null && tab.id === id);
  updateToolsForFocus();
}

/** The one toolbar acts on the focused pane, so it has to show that pane's settings */
export function updateToolsForFocus() {
  const tab = focusedTab();
  const view = tab?.view ?? prefs.tab;
  for (const button of ui.tabs.querySelectorAll("[data-tab]")) {
    button.setAttribute("aria-selected", String(button.getAttribute("data-tab") === view));
  }
  ui.treeTools.hidden = view !== "tree";
}

export function renderPaneHead(tab) {
  if (!tab?.el) return;
  const { row } = tab;
  const { record } = row;
  /** @type {{text: string, title?: string}[]} */
  const meta = [{ text: `${formatDate(record.time)} ${row.time}`, title: `Shown in ${TIME_ZONE}` }];
  if (record.delta != null) {
    meta.push({ text: `Δ ${formatDelta(record.delta)}`, title: "Gap to the request before this one" });
  }
  meta.push({ text: `RPC ID ${record.rpc_id}` }, { text: `RPC status ${record.rpc_status}` });
  if (record.rpc_handle != null) meta.push({ text: `RPC handle ${record.rpc_handle}` });
  if (record.protos.length > 1) meta.push({ text: `proto ${row.index + 1} of ${record.protos.length}` });

  const saved = state.saved.has(record.id);
  tab.el.querySelector(".pane-head").innerHTML =
    '<div class="ih-title">' +
    `<span class="ih-method">${escapeHtml(row.method)}</span>` +
    `<span class="ih-value">${row.value}</span>` +
    (row.badge ? badgeHtml(row.badge) : "") +
    '<span class="ih-actions">' +
    `<button class="mini${saved ? " on" : ""}" type="button" data-action="save" ` +
    `title="${saved ? "Stop keeping this request" : "Keep this request in the log (b)"}">` +
    `${saved ? "Saved" : "Save"}</button>` +
    '<button class="mini" type="button" data-action="link" title="Copy a link to this proto">Link</button>' +
    '<button class="mini" type="button" data-action="hide-method" title="Hide this method (h)">Hide method</button>' +
    '<button class="mini" type="button" data-action="only-method" title="Only show this method">Only this method</button>' +
    "</span></div>" +
    (row.via ? `<div class="ih-via">via ${escapeHtml(row.via)}</div>` : "") +
    `<div class="ih-meta">${meta
      .map((item) => `<span${item.title ? ` title="${escapeHtml(item.title)}"` : ""}>${escapeHtml(item.text)}</span>`)
      .join('<span class="sep">·</span>')}</div>`;
  renderPaneNote(tab);
}

/** The filter can hide the proto that is open, or the log can drop it. Say so where you are looking */
function renderPaneNote(tab) {
  const note = tab.el?.querySelector(".pane-note");
  if (!note) return;
  const text = tab.stale || tab.row.dropped
    ? "This proto is no longer in the log, it was cleared or dropped"
    : matches(tab.row)
      ? null
      : "This proto is hidden by the current filters";
  note.hidden = text === null;
  if (text !== null) note.textContent = text;
}

export function updateInspectorNote() {
  for (const tab of tabs()) if (isVisible(tab)) renderPaneNote(tab);
}

/** saved.js and the record list call this when something about the row changed */
export function renderInspectorHead(row) {
  for (const tab of tabs()) {
    if (isVisible(tab) && tab.row === row) renderPaneHead(tab);
  }
}

export function renderPaneBody(tab) {
  if (!tab?.el) return;
  const body = tab.el.querySelector(".pane-body");

  if (tab.error) {
    body.innerHTML = `<div class="inspector-message">${escapeHtml(tab.error)}</div>`;
    return;
  }
  const detail = tab.detail;
  if (!detail) return;

  const terms = [...highlightTerms(), ...findTerms(tab)];
  const context = { terms, matcher: makeMatcher(terms), matchesOnly: tab.find.matchesOnly && Boolean(tab.find.query) };
  const inner = detail.proxy || detail;
  const sections = [
    makeSection(tab, "Request", inner.request, true, context),
    makeSection(tab, "Response", inner.response, true, context),
  ];
  if (detail.proxy) {
    sections.push(
      makeSection(tab, "Proxy request", detail.request, false, context),
      makeSection(tab, "Proxy response", detail.response, false, context),
    );
  }

  const sameDetail = tab.renderedDetail === detail;
  const scrollTop = body.scrollTop;
  body.classList.toggle("no-types", !prefs.types);
  body.classList.remove("loading");
  body.replaceChildren(...sections);
  tab.renderedDetail = detail;

  if (!restoreFindPosition(tab, body)) {
    // without a find, a filter match is still worth scrolling to
    const mark = terms.length ? body.querySelector("mark:not(.find)") : null;
    if (mark) mark.scrollIntoView({ block: "center" });
    else body.scrollTop = sameDetail ? scrollTop : 0;
  }
}

/** Re-renders whichever pane the toolbar is pointing at */
export function renderInspectorBody() {
  const tab = focusedTab();
  if (tab) renderPaneBody(tab);
}

export function renderVisiblePanes() {
  for (const tab of tabs()) if (isVisible(tab)) renderPaneBody(tab);
}

// ------------------------------------------------------------------ find inside a proto

/** Find terms look like filter terms so highlight() and makeMatcher() work on both */
function findTerms(tab) {
  const { query, caseSensitive, regex } = tab.find;
  if (query.length < 2) return [];
  const source = regex ? query : query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const flags = caseSensitive ? "" : "i";
  try {
    return [{ value: query, test: new RegExp(source, flags), global: new RegExp(source, flags + "g"), cls: "find" }];
  } catch {
    return [];
  }
}

export function findIsValid(tab) {
  if (!tab.find.regex || tab.find.query.length < 2) return true;
  try {
    new RegExp(tab.find.query);
    return true;
  } catch {
    return false;
  }
}

/** Marks of one match can be split across syntax spans, so they are grouped by data-m */
export function findMatches(tab) {
  const body = tab?.el?.querySelector(".pane-body");
  if (!body) return [];
  const groups = [];
  let last = null;
  for (const mark of body.querySelectorAll("mark.find")) {
    const index = mark.getAttribute("data-m");
    if (index !== null && index === last) continue;
    last = index;
    groups.push(mark);
  }
  return groups;
}

function restoreFindPosition(tab, body) {
  if (!tab.find.query) return false;
  const found = findMatches(tab);
  if (!found.length) return false;
  tab.find.index = Math.min(tab.find.index, found.length - 1);
  const current = found[tab.find.index];
  for (const mark of body.querySelectorAll("mark.find.current")) mark.classList.remove("current");
  current.classList.add("current");
  current.scrollIntoView({ block: "center" });
  return true;
}

export function stepFind(tab, delta) {
  const found = findMatches(tab);
  if (!found.length) return;
  tab.find.index = (tab.find.index + delta + found.length) % found.length;
  const body = tab.el?.querySelector(".pane-body");
  for (const mark of body.querySelectorAll("mark.find.current")) mark.classList.remove("current");
  const current = found[tab.find.index];
  current.classList.add("current");
  current.scrollIntoView({ block: "center" });
}

// ------------------------------------------------------------------ sections

export function makeSection(tab, name, message, openByDefault, context) {
  const section = document.createElement("section");
  section.className = "section";
  section.innerHTML =
    '<div class="section-head">' +
    '<span class="tw"></span>' +
    `<span class="section-title">${name}</span>` +
    `<span class="section-name">${escapeHtml(message.name || "Unknown message")}</span>` +
    messageStateBadge(message.state) +
    `<span class="section-size">${formatBytes(message.size)}</span>` +
    '<span class="section-actions">' +
    '<button class="mini" type="button" data-copy="json" title="Copy as JSON">JSON</button>' +
    '<button class="mini" type="button" data-copy="text" title="Copy as protobuf text">Text</button>' +
    '<button class="mini" type="button" data-copy="base64" title="Copy the raw message as base64">Base64</button>' +
    "</span></div>" +
    '<div class="section-body"></div>';
  section._name = name;
  section._message = message;
  section._context = context;
  section._tab = tab;
  // a find has to look inside every section, otherwise it would miss matches that are folded away
  const open = context.terms.some((term) => term.cls === "find") ? true : tab.sectionState.get(name) ?? openByDefault;
  setSectionOpen(section, open);
  return section;
}

function messageStateBadge(messageState) {
  const badges = {
    unknown: { text: "unknown type", tone: "warn" },
    error: { text: "decode error", tone: "bad" },
    empty: { text: "empty", tone: "muted" },
  };
  return badges[messageState] ? badgeHtml(badges[messageState]) : "";
}

export function setSectionOpen(section, open) {
  if (open && !section._rendered) {
    section._rendered = true;
    renderSectionBody(section.lastElementChild, section._message, section._context, section._tab);
  }
  section.classList.toggle("open", open);
  section._tab?.sectionState.set(section._name, open);
}

function renderSectionBody(body, message, context, tab) {
  if (message.state === "unknown") {
    body.insertAdjacentHTML(
      "beforeend",
      "<div class=\"note-box\">Traffic Light doesn't know this message, this is a best guess of its structure.</div>",
    );
  } else if (message.state === "error") {
    body.insertAdjacentHTML(
      "beforeend",
      `<div class="note-box">Couldn't decode this as ${escapeHtml(message.name)}, this is a best guess of its structure.</div>`,
    );
  } else if (message.state === "empty") {
    body.insertAdjacentHTML("beforeend", '<div class="empty-message">No fields set</div>');
    return;
  }

  const view = tab?.view ?? prefs.tab;
  if (view === "tree") {
    body.append(renderTree(message.tree || jsonToNode(message.json), context));
    return;
  }

  const blackbox = !message.tree;
  let source = view === "json" ? JSON.stringify(message.json ?? null, null, 2) : message.text;
  let folded = false;
  if (context.matchesOnly) {
    const kept = source.split("\n").filter((line) => context.terms.some((term) => term.test.test(line)));
    folded = kept.length !== source.split("\n").length;
    source = kept.length ? kept.join("\n") : "";
  }
  const truncated = source.length > CODE_LIMIT;
  const [regex, kinds] = view === "json" || blackbox ? JSON_SYNTAX : TEXT_SYNTAX;
  body.insertAdjacentHTML(
    "beforeend",
    (source
      ? `<pre class="code">${highlightCode(truncated ? source.slice(0, CODE_LIMIT) : source, regex, kinds, context.terms)}</pre>`
      : '<div class="empty-message">No line matches</div>') +
      (folded && source ? '<div class="note-box">Only the lines that match are shown.</div>' : "") +
      (truncated ? '<div class="note-box">Too long to show completely. The copy buttons copy everything.</div>' : ""),
  );
}

export function setTreeOpen(open) {
  const tab = focusedTab();
  if (!tab?.el) return;
  for (const section of tab.el.querySelectorAll(".section")) {
    if (open) setSectionOpen(section, true);
    for (const node of section.querySelectorAll(".tree > .tn")) setNodeOpenDeep(node, open);
  }
}

/** The view belongs to the pane, so two panes can show the same proto as a tree and as JSON */
export function setTab(view) {
  if (!TABS.includes(view)) return;
  prefs.tab = view;
  savePrefs();
  const tab = focusedTab();
  if (tab) {
    tab.view = view;
    tab.sectionState = new Map();
    tab.renderedDetail = null;
    renderPaneBody(tab);
  }
  updateToolsForFocus();
}

// @ts-check
import { copy, copyInspected } from "./clipboard.js";
import { SORT_FIRST_DESC, TABS, THEMES, TIME_MODES } from "./constants.js";
import {
  activeTab,
  clearInspector,
  closeActiveTab,
  closeTab,
  cycleTab,
  detailCache,
  findIsValid,
  findMatches,
  focusedTab,
  focusPane,
  openInspect,
  rebindTabs,
  renderInspectorBody,
  renderPaneBody,
  renderPanes,
  renderTabs,
  renderVisiblePanes,
  setSectionOpen,
  setDiff,
  setSplit,
  setTab,
  setTreeOpen,
  showInSplit,
  stepFind,
  toggleDiffContext,
} from "./inspector.js";
import { jumpToSelected, moveSelection, moveTo, pageSize, refilter, renderEmpty, renderList, scheduleRender, select, setFollow, setSort, setTimeMode, updateSortUi, updateTimeModeUi } from "./list.js";
import { hideInspectedMethod, methodFilterChanged, renderMethods, setMethodFilter, toggleMethod, toggleMethods, updateMethodsBadge } from "./methods.js";
import { applySaved, applySavedSet, toggleSaved, updateSavedChip } from "./saved.js";
import { closePopovers, openPopover, toggleHelp } from "./popover.js";
import { prefs, savePrefs } from "./prefs.js";
import { parseQuery } from "./query.js";
import { addRecords, dropRecords, resetRecords } from "./records.js";
import { acceptSuggestion, applySearch, focusSearch, hideSuggestions, moveSuggestion, setSearch, toggleSavedFilter, updateSuggestions } from "./search.js";
import { connect, send } from "./socket.js";
import { edgePosition, followEdge } from "./sort.js";
import { state } from "./state.js";
import { applyTheme, switchTheme } from "./theme.js";
import { setNodeOpen, setNodeOpenDeep } from "./tree.js";
import { toast, ui } from "./ui.js";
import { clamp, debounce, numberFormat, plural } from "./util.js";
/** @typedef {import("./types.js").Token} Token */

if (!(prefs.sort?.key in SORT_FIRST_DESC) || typeof prefs.sort.desc !== "boolean") {
  prefs.sort = { key: "time", desc: false };
}
if (!THEMES.includes(prefs.theme)) prefs.theme = "system";
if (!TABS.includes(prefs.tab)) prefs.tab = "tree";
if (!TIME_MODES.includes(prefs.timeMode)) prefs.timeMode = "clock";
if (typeof prefs.query !== "string") prefs.query = "";

ui.search.addEventListener("input", () => {
  ui.searchClear.hidden = !ui.search.value;
  applySearch();
  updateSuggestions();
});

ui.search.addEventListener("keydown", (event) => {
  const suggesting = !ui.suggestions.hidden;
  switch (event.key) {
    case "ArrowDown":
    case "ArrowUp":
      event.preventDefault();
      if (suggesting) moveSuggestion(event.key === "ArrowDown" ? 1 : -1);
      else {
        applySearch.flush();
        moveSelection(event.key === "ArrowDown" ? 1 : -1);
      }
      break;
    case "Tab":
      if (suggesting) {
        event.preventDefault();
        acceptSuggestion();
      }
      break;
    case "Enter":
      event.preventDefault();
      if (suggesting) acceptSuggestion();
      else applySearch.flush();
      break;
    case "Escape":
      event.preventDefault();
      event.stopPropagation();
      if (suggesting) hideSuggestions();
      else if (ui.search.value) setSearch("");
      else ui.search.blur();
      break;
  }
});

ui.search.addEventListener("click", updateSuggestions);
ui.search.addEventListener("keyup", (event) => {
  if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) updateSuggestions();
});
ui.search.addEventListener("blur", hideSuggestions);
// keeps the focus in the search box, so the click below still knows where to insert
ui.suggestions.addEventListener("mousedown", (event) => event.preventDefault());
ui.suggestions.addEventListener("click", (event) => {
  const item = event.target.closest("li[data-index]");
  if (item) acceptSuggestion(Number(item.dataset.index));
});
ui.searchClear.addEventListener("click", () => {
  setSearch("");
  ui.search.focus();
});

ui.methodsBtn.addEventListener("click", toggleMethods);
ui.methodsSearch.addEventListener("input", renderMethods);
ui.methodsAll.addEventListener("click", () => setMethodFilter("exclude", []));
ui.methodsNone.addEventListener("click", () => setMethodFilter("include", []));
ui.methodsList.addEventListener("click", (event) => {
  const item = event.target.closest("li[data-method]");
  if (!item) return;
  if (event.target.closest(".only")) setMethodFilter("include", [item.dataset.method]);
  else toggleMethod(item.dataset.method);
});

document.addEventListener("pointerdown", (event) => {
  for (const popover of [ui.methodsPop, ui.helpPop, ui.exportPop, ui.clearPop]) {
    if (!popover.hidden && !popover.contains(event.target) && !popover._anchor?.contains(event.target)) {
      popover.hidden = true;
    }
  }
});

export function setFirstOnly(value) {
  prefs.firstOnly = value;
  savePrefs();
  ui.firstOnly.checked = value;
  refilter();
}

export function setPaused(paused) {
  state.paused = paused;
  ui.pauseBtn.setAttribute("aria-pressed", String(paused));
  ui.pauseBtn.title = paused ? "Resume capturing (p)" : "Pause capturing (p)";
  ui.pausedNote.hidden = !paused;
  renderEmpty();
}

export function resetFilters() {
  ui.search.value = "";
  ui.searchClear.hidden = true;
  ui.search.classList.remove("invalid");
  prefs.query = "";
  state.query = parseQuery("");
  prefs.firstOnly = false;
  ui.firstOnly.checked = false;
  state.methodFilter = { mode: "exclude", set: new Set() };
  methodFilterChanged();
  renderInspectorBody();
}


export function applyListWidth() {
  if (prefs.listWidth) ui.main.style.setProperty("--list-width", `${prefs.listWidth * 100}%`);
  else ui.main.style.removeProperty("--list-width");
}

ui.firstOnly.addEventListener("change", () => setFirstOnly(ui.firstOnly.checked));
ui.pauseBtn.addEventListener("click", () => send({ type: "pause", value: !state.paused }));
ui.follow.addEventListener("change", () => setFollow(ui.follow.checked));
ui.clearBtn.addEventListener("click", () => {
  // with nothing saved there is only one thing clearing can mean, so don't ask
  if (!state.saved.size) {
    send({ type: "clear" });
    return;
  }
  if (!ui.clearPop.hidden) {
    ui.clearPop.hidden = true;
    return;
  }
  ui.clearKeeps.textContent = `keeps ${numberFormat.format(state.saved.size)}`;
  openPopover(ui.clearPop, ui.clearBtn);
});
ui.clearKeepSaved.addEventListener("click", () => {
  ui.clearPop.hidden = true;
  send({ type: "clear" });
});
ui.clearEverything.addEventListener("click", () => {
  ui.clearPop.hidden = true;
  send({ type: "clear", saved: true });
});
ui.helpBtn.addEventListener("click", () => toggleHelp(ui.helpBtn));
ui.shortcutsBtn.addEventListener("click", () => toggleHelp(ui.shortcutsBtn));
ui.themeBtn.addEventListener("click", switchTheme);
ui.exportBtn.addEventListener("click", () => {
  if (!ui.exportPop.hidden) {
    ui.exportPop.hidden = true;
    return;
  }
  const visibleRecords = new Set(state.visible.map((row) => row.record.id));
  ui.exportAllCount.textContent = numberFormat.format(state.records.length);
  ui.exportFilteredCount.textContent = numberFormat.format(visibleRecords.size);
  ui.exportSavedCount.textContent = numberFormat.format(state.saved.size);
  ui.exportFiltered.disabled = visibleRecords.size === 0;
  ui.exportSaved.classList.toggle("disabled", state.saved.size === 0);
  openPopover(ui.exportPop, ui.exportBtn);
});

ui.exportAll.addEventListener("click", () => {
  ui.exportPop.hidden = true;
  if (state.records.length) toast(`Exporting ${plural(state.records.length, "request")}`);
});
ui.exportSaved.addEventListener("click", (event) => {
  ui.exportPop.hidden = true;
  if (!state.saved.size) {
    event.preventDefault();
    toast("Nothing is saved yet");
  }
});

// the filtered set is only known here, so it goes to the server as a list of ids
ui.exportFiltered.addEventListener("click", async () => {
  ui.exportPop.hidden = true;
  const ids = [...new Set(state.visible.map((row) => row.record.id))];
  if (!ids.length) {
    toast("The filter leaves nothing to export");
    return;
  }

  toast(`Exporting ${plural(ids.length, "request")}`);
  try {
    const response = await fetch("api/export", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ids }),
    });
    if (response.status === 401) {
      location.assign("login");
      return;
    }
    if (!response.ok) throw new Error(`HTTP ${response.status}`);

    const url = URL.createObjectURL(await response.blob());
    const link = document.createElement("a");
    link.href = url;
    link.download = exportFilename();
    link.click();
    URL.revokeObjectURL(url);
  } catch (error) {
    toast("Couldn't export the filtered log");
  }
});

// the same name the server picks for the other two
function exportFilename() {
  const now = new Date();
  const two = (value) => String(value).padStart(2, "0");
  return (
    `trafficlight-${now.getFullYear()}-${two(now.getMonth() + 1)}-${two(now.getDate())}-` +
    `${two(now.getHours())}${two(now.getMinutes())}${two(now.getSeconds())}.json`
  );
}
ui.receiver.addEventListener("click", () => copy(state.receiver, "the receiver address"));
ui.logoutBtn.addEventListener("click", () => {
  fetch("logout", { method: "POST" }).finally(() => location.assign("login"));
});
ui.newPill.addEventListener("click", () => setFollow(true));
ui.savedChip.addEventListener("click", toggleSavedFilter);
ui.jumpPill.addEventListener("click", jumpToSelected);

ui.logEmpty.addEventListener("click", (event) => {
  const action = event.target.closest("[data-action]")?.dataset.action;
  if (action === "resume") send({ type: "pause", value: false });
  else if (action === "reset-filters") resetFilters();
});

ui.timeMode.addEventListener("click", () => setTimeMode(prefs.timeMode === "delta" ? "clock" : "delta"));

ui.logHead.addEventListener("click", (event) => {
  const button = event.target.closest("[data-sort]");
  if (button) setSort(button.dataset.sort);
});

ui.rows.addEventListener("click", (event) => {
  const element = event.target.closest(".row");
  if (!element) return;
  const row = state.visible[Number(element.dataset.position)];

  if (event.target.closest(".save")) {
    toggleSaved(row);
    return;
  }
  // dragging across a row to copy its preview shouldn't also select it
  if (window.getSelection()?.toString()) return;

  // inspecting an older row stops following, otherwise it would scroll away
  if (Number(element.dataset.position) !== edgePosition()) setFollow(false);
  // ctrl or cmd click opens it behind what you are reading, like a browser tab
  if (event.ctrlKey || event.metaKey) openInspect(row, "background");
  else select(row);
});

// a double click keeps the tab instead of letting the next click replace it
ui.rows.addEventListener("dblclick", (event) => {
  const element = event.target.closest(".row");
  if (element && !event.target.closest(".save")) select(state.visible[Number(element.dataset.position)], "permanent");
});

// middle click fires auxclick, never click
ui.rows.addEventListener("auxclick", (event) => {
  if (event.button !== 1) return;
  const element = event.target.closest(".row");
  if (!element) return;
  event.preventDefault();
  openInspect(state.visible[Number(element.dataset.position)], "background");
});
ui.rows.addEventListener("mousedown", (event) => {
  // stops the middle click autoscroll cursor
  if (event.button === 1) event.preventDefault();
});

// --- the tab strip
ui.docTabs.addEventListener("click", (event) => {
  const element = event.target.closest(".doc-tab");
  if (!element) return;
  const id = Number(element.dataset.tab);
  if (event.target.closest("[data-close]")) closeTab(id);
  else if (event.altKey) showInSplit(id);
  else {
    const tab = state.panes.tabs.find((candidate) => candidate.id === id);
    if (tab) select(tab.row, "permanent");
  }
});
ui.docTabs.addEventListener("auxclick", (event) => {
  const element = event.target.closest(".doc-tab");
  if (element && event.button === 1) {
    event.preventDefault();
    closeTab(Number(element.dataset.tab));
  }
});
ui.splitBtn.addEventListener("click", () => setSplit(state.panes.splitId === null));
ui.diffBtn.addEventListener("click", () => setDiff(!state.panes.diff));

ui.viewport.addEventListener(
  "scroll",
  () => {
    const viewport = ui.viewport;
    // following means sticking to where new rows show up: scrolling away stops it, scrolling back starts it again
    const edge = followEdge();
    if (edge) {
      const atEdge =
        edge === "top" ? viewport.scrollTop <= 4 : viewport.scrollTop + viewport.clientHeight >= viewport.scrollHeight - 4;
      if (atEdge !== state.follow) setFollow(atEdge);
    }
    scheduleRender();
  },
  { passive: true },
);
new ResizeObserver(scheduleRender).observe(ui.viewport);

ui.splitter.addEventListener("pointerdown", (event) => {
  event.preventDefault();
  const splitter = ui.splitter;
  const bounds = ui.main.getBoundingClientRect();
  splitter.setPointerCapture(event.pointerId);
  splitter.classList.add("dragging");

  const move = (moveEvent) => {
    prefs.listWidth = clamp((moveEvent.clientX - bounds.left) / bounds.width, 0.2, 0.8);
    applyListWidth();
  };
  const stop = () => {
    splitter.classList.remove("dragging");
    splitter.removeEventListener("pointermove", move);
    splitter.removeEventListener("pointerup", stop);
    splitter.removeEventListener("pointercancel", stop);
    savePrefs();
  };
  splitter.addEventListener("pointermove", move);
  splitter.addEventListener("pointerup", stop);
  splitter.addEventListener("pointercancel", stop);
});
ui.splitter.addEventListener("dblclick", () => {
  prefs.listWidth = null;
  applyListWidth();
  savePrefs();
});

ui.tabs.addEventListener("click", (event) => {
  const button = event.target.closest("[data-tab]");
  if (button) setTab(button.dataset.tab);
});
ui.typesToggle.addEventListener("change", () => {
  prefs.types = ui.typesToggle.checked;
  savePrefs();
  for (const body of ui.panes.querySelectorAll(".pane-body")) body.classList.toggle("no-types", !prefs.types);
});
ui.expandBtn.addEventListener("click", () => setTreeOpen(true));
ui.collapseBtn.addEventListener("click", () => setTreeOpen(false));
ui.copyJsonBtn.addEventListener("click", () => copyInspected("json"));
ui.copyTextBtn.addEventListener("click", () => copyInspected("text"));

ui.panes.addEventListener("pointerdown", (event) => {
  const pane = event.target.closest(".pane");
  if (pane) focusPane(Number(pane.dataset.tab));
});

ui.panes.addEventListener("click", (event) => {
  const pane = event.target.closest(".pane");
  const tab = pane ? state.panes.tabs.find((candidate) => candidate.id === Number(pane.dataset.tab)) : null;

  const action = event.target.closest(".pane-head [data-action]")?.dataset.action;
  if (action) {
    const row = tab?.row;
    if (action === "diff-context") toggleDiffContext();
    else if (action === "save") toggleSaved(row);
    else if (action === "link") copyProtoLink(row);
    else if (action === "hide-method") hideInspectedMethod();
    else if (action === "only-method" && row) setMethodFilter("include", [row.method]);
    return;
  }
  const copyButton = event.target.closest("[data-copy]");
  if (copyButton) {
    const message = copyButton.closest(".section")._message;
    const format = copyButton.dataset.copy;
    const text =
      format === "json" ? JSON.stringify(message.json ?? null, null, 2) : format === "text" ? message.text : message.base64;
    copy(text, `${message.name || "message"} as ${format === "json" ? "JSON" : format}`);
    return;
  }

  const head = event.target.closest(".section-head");
  if (head) {
    setSectionOpen(head.parentElement, !head.parentElement.classList.contains("open"));
    return;
  }

  // selecting text shouldn't fold the line it's on
  const line = event.target.closest(".tl.x");
  if (line && !window.getSelection().toString()) {
    const node = line.parentElement;
    const open = !node.classList.contains("open");
    if (event.altKey) setNodeOpenDeep(node, open);
    else setNodeOpen(node, open);
  }
});

// ------------------------------------------------------------------ find inside a proto

export function openFind() {
  const tab = focusedTab();
  if (!tab) {
    toast("Open a proto first");
    return;
  }
  ui.findBar.hidden = false;
  syncFindBar();
  ui.findInput.focus();
  ui.findInput.select();
}

function closeFind() {
  ui.findBar.hidden = true;
  const tab = focusedTab();
  if (tab && tab.find.query) {
    tab.find.query = "";
    renderPaneBody(tab);
  }
  updateFindCount();
}

/** The bar belongs to the focused pane, so switching panes shows that pane's search */
export function syncFindBar() {
  const tab = focusedTab();
  if (!tab) return;
  ui.findInput.value = tab.find.query;
  ui.findCase.setAttribute("aria-pressed", String(tab.find.caseSensitive));
  ui.findRegex.setAttribute("aria-pressed", String(tab.find.regex));
  ui.findOnly.setAttribute("aria-pressed", String(tab.find.matchesOnly));
  updateFindCount();
}

function updateFindCount() {
  const tab = focusedTab();
  if (!tab) {
    ui.findCount.textContent = "";
    return;
  }
  const valid = findIsValid(tab);
  ui.findInput.classList.toggle("invalid", !valid);
  if (!tab.find.query) ui.findCount.textContent = "";
  else if (tab.find.query.length < 2) ui.findCount.textContent = "2+ chars";
  else if (!valid) ui.findCount.textContent = "bad regex";
  else {
    const total = findMatches(tab).length;
    ui.findCount.textContent = total ? `${tab.find.index + 1}/${total}` : "no match";
  }
}

// re-rendering a pane for every keystroke is the expensive part, so it waits for a pause
const applyFind = debounce(() => {
  const tab = focusedTab();
  if (!tab) return;
  tab.find.index = 0;
  renderPaneBody(tab);
  updateFindCount();
}, 250);

document.addEventListener("tabchange", () => {
  if (!ui.findBar.hidden) syncFindBar();
  rememberHash();
});

ui.findBtn.addEventListener("click", () => (ui.findBar.hidden ? openFind() : closeFind()));
ui.findClose.addEventListener("click", closeFind);
ui.findInput.addEventListener("input", () => {
  const tab = focusedTab();
  if (!tab) return;
  tab.find.query = ui.findInput.value;
  applyFind();
});
function findToggle(element, read, write) {
  element.addEventListener("click", () => {
    const tab = focusedTab();
    if (!tab) return;
    const value = !read(tab.find);
    write(tab.find, value);
    element.setAttribute("aria-pressed", String(value));
    applyFind();
    applyFind.flush();
  });
}
findToggle(ui.findCase, (find) => find.caseSensitive, (find, value) => (find.caseSensitive = value));
findToggle(ui.findRegex, (find) => find.regex, (find, value) => (find.regex = value));
findToggle(ui.findOnly, (find) => find.matchesOnly, (find, value) => (find.matchesOnly = value));
ui.findNext.addEventListener("click", () => stepFocusedFind(1));
ui.findPrev.addEventListener("click", () => stepFocusedFind(-1));

function stepFocusedFind(delta) {
  const tab = focusedTab();
  if (!tab) return;
  applyFind.flush();
  stepFind(tab, delta);
  updateFindCount();
}

ui.findInput.addEventListener("keydown", (event) => {
  if (event.key === "Enter") {
    event.preventDefault();
    stepFocusedFind(event.shiftKey ? -1 : 1);
  } else if (event.key === "Escape") {
    event.preventDefault();
    event.stopPropagation();
    closeFind();
  }
});

// ------------------------------------------------------------------ links to a proto

const protoHash = (row) => `#r${row.record.id}:${row.index}`;

export function rememberHash() {
  const tab = activeTab();
  const hash = tab ? protoHash(tab.row) : "";
  if (location.hash !== hash) history.replaceState(null, "", hash || location.pathname);
}

function copyProtoLink(row) {
  if (!row) return;
  copy(location.origin + location.pathname + protoHash(row), "a link to this proto");
}

let pendingHash = null;

/** Record ids only mean something while this Traffic Light is running, so a link is best effort */
export function resolveHash() {
  if (!pendingHash) return;
  const row = state.rows.find((candidate) => candidate.key === pendingHash);
  if (!row) return;
  pendingHash = null;
  select(row, "permanent");
}

function readHash() {
  const match = /^#r(\d+):(\d+)$/.exec(location.hash);
  pendingHash = match ? `${match[1]}:${match[2]}` : null;
  resolveHash();
}
window.addEventListener("hashchange", readHash);

const shortcuts = {
  "/": focusSearch,
  ArrowDown: () => moveSelection(1),
  j: () => moveSelection(1),
  ArrowUp: () => moveSelection(-1),
  k: () => moveSelection(-1),
  PageDown: () => moveSelection(pageSize()),
  PageUp: () => moveSelection(-pageSize()),
  Home: () => {
    moveTo(0);
    if (followEdge() === "top") setFollow(true);
  },
  End: () => {
    moveTo(Infinity);
    if (followEdge() === "bottom") setFollow(true);
  },
  p: () => send({ type: "pause", value: !state.paused }),
  f: () => {
    if (followEdge()) setFollow(!state.follow);
    else toast("Following only works when sorting by time");
  },
  g: jumpToSelected,
  Enter: () => select(state.selected, "permanent"),
  b: () => toggleSaved(state.inspected),
  w: closeActiveTab,
  "[": () => cycleTab(-1),
  "]": () => cycleTab(1),
  n: () => stepFocusedFind(1),
  1: () => setFirstOnly(!prefs.firstOnly),
  m: toggleMethods,
  h: hideInspectedMethod,
  c: () => copyInspected("json"),
  t: () => setTab(TABS[(TABS.indexOf(prefs.tab) + 1) % TABS.length]),
  "?": toggleHelp,
};

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") {
    if (closePopovers()) event.preventDefault();
    return;
  }
  // checkboxes keep the focus after a click, shortcuts should keep working then
  if (event.isComposing || event.target.closest?.("input:not([type=checkbox]), textarea, select")) return;
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
    event.preventDefault();
    focusSearch();
    return;
  }
  // this is a devtool, so find means find in the proto you are reading
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "f" && state.panes.tabs.length) {
    event.preventDefault();
    openFind();
    return;
  }
  if (event.ctrlKey || event.metaKey || event.altKey) return;

  const shortcut = shortcuts[event.key];
  if (!shortcut) return;
  event.preventDefault();
  shortcut();
});

export function handleMessage(message) {
  switch (message.type) {
    case "hello": {
      // the server sends its whole log after this. After a restart, nothing from before is valid anymore
      const restarted = state.session !== null && state.session !== message.session;
      state.restoreKey = restarted ? null : state.selected?.key ?? null;
      state.restoreFollow = state.follow;
      if (restarted) {
        detailCache.clear();
        clearInspector();
      }
      state.session = message.session;
      state.maxRecords = message.max_records;
      state.receiver = message.receiver;
      ui.receiverUrl.textContent = message.receiver;
      ui.receiver.hidden = false;
      ui.logoutBtn.hidden = !message.auth;
      setPaused(message.paused);
      resetRecords();
      applySavedSet(message.saved ?? []);
      break;
    }
    case "history":
      addRecords(message.records, true, message.dropped);
      rebindTabs();
      resolveHash();
      break;
    case "records":
      addRecords(message.records, false, message.dropped);
      resolveHash();
      break;
    case "drop":
      dropRecords(message.ids ?? []);
      scheduleRender();
      break;
    case "save":
      applySaved(message.id, message.value);
      break;
    case "saved":
      applySavedSet(message.ids ?? []);
      break;
    case "notice":
      toast(message.text);
      break;
    case "clear": {
      // saved requests survive a clear, so they come back in the same message
      const kept = message.records ?? [];
      state.restoreKey = state.selected?.key ?? null;
      state.restoreFollow = state.follow;
      resetRecords();
      if (kept.length) addRecords(kept, true);
      rebindTabs();
      if (!state.selected) clearInspector();
      toast(kept.length ? `Log cleared, ${plural(kept.length, "saved request")} kept` : "Log cleared");
      break;
    }
    case "state":
      setPaused(message.paused);
      break;
  }
}

state.query = parseQuery(prefs.query);
ui.search.value = prefs.query;
ui.searchClear.hidden = !prefs.query;
ui.search.classList.toggle("invalid", Boolean(state.query.error));
ui.firstOnly.checked = prefs.firstOnly;
ui.typesToggle.checked = prefs.types;
setTab(prefs.tab);
updateSortUi();
updateTimeModeUi();
setFollow(state.follow);
applyTheme();
applyListWidth();
updateMethodsBadge();
updateSavedChip();
renderTabs();
renderPanes();
readHash();
renderList();
connect(handleMessage);

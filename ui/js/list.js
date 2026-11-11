// @ts-check
import { badgeHtml } from "./badge.js";
import { OVERSCAN, ROW_HEIGHT, SORT_FIRST_DESC } from "./constants.js";
import { openInspect, updateInspectorNote } from "./inspector.js";
import { prefs, savePrefs } from "./prefs.js";
import { matches } from "./query.js";
import { compareRows, edgePosition, followEdge, insertionPoint, sortedByArrival } from "./sort.js";
import { state } from "./state.js";
import { toast, ui } from "./ui.js";
import { clamp, debounce, escapeHtml, formatBytes, formatDelta, formatFullTime, numberFormat, plural } from "./util.js";

export function refilter() {
  state.visible = state.rows.filter(matches);
  if (!sortedByArrival()) state.visible.sort(compareRows);
  state.unseen = 0;
  updateNewPill();
  updateInspectorNote();
  updateSizer();

  // keep the selection in sight
  if (!state.follow && state.selected) {
    const position = state.visible.indexOf(state.selected);
    if (position !== -1) center(position);
  }
  scheduleRender();
}

export function insertVisible(rows) {
  if (sortedByArrival()) {
    for (const row of rows) state.visible.push(row);
  } else if (rows.length > 64) {
    for (const row of rows) state.visible.push(row);
    state.visible.sort(compareRows);
  } else {
    for (const row of rows) state.visible.splice(insertionPoint(row), 0, row);
  }
}

// keeps the rows in view where they are while rows are added or removed above them
export function keepInView(change) {
  const viewport = ui.viewport;
  const anchorPosition = Math.floor(viewport.scrollTop / ROW_HEIGHT);
  const anchor = state.follow ? null : state.visible[anchorPosition];
  const offset = viewport.scrollTop - anchorPosition * ROW_HEIGHT;
  change();

  const position = anchor ? state.visible.indexOf(anchor) : -1;
  if (position !== -1 && position !== anchorPosition) {
    updateSizer();
    viewport.scrollTop = position * ROW_HEIGHT + offset;
  }
}

export function setSort(key) {
  prefs.sort = { key, desc: key === prefs.sort.key ? !prefs.sort.desc : SORT_FIRST_DESC[key] };
  savePrefs();
  updateSortUi();

  // show the selection, otherwise the newest rows, otherwise the top
  const edge = followEdge();
  setFollow(!state.selected && edge !== null);
  refilter();
  if (!state.selected && !edge) ui.viewport.scrollTop = 0;
}

export function setTimeMode(mode) {
  prefs.timeMode = mode;
  savePrefs();
  updateTimeModeUi();
  // every row renders its first column differently now
  for (const row of state.rows) row.html = null;
  scheduleRender();
}

export function updateTimeModeUi() {
  const delta = prefs.timeMode === "delta";
  ui.timeMode.classList.toggle("on", delta);
  ui.timeMode.title = delta ? "Show the time of day instead" : "Show the gap to the request before instead";
  ui.timeLabel.textContent = delta ? "Δ prev" : "Time";
}

export function updateSortUi() {
  for (const button of ui.logHead.querySelectorAll("[data-sort]")) {
    const active = button.dataset.sort === prefs.sort.key;
    button.classList.toggle("active", active);
    button.classList.toggle("desc", active ? prefs.sort.desc : SORT_FIRST_DESC[button.dataset.sort]);
  }

  const canFollow = followEdge() !== null;
  const label = ui.follow.closest(".check");
  ui.follow.disabled = !canFollow;
  label.classList.toggle("disabled", !canFollow);
  label.title = canFollow ? "Keep scrolling to new requests (f)" : "Following only works when sorting by time";
}

let renderQueued = false;
export function scheduleRender() {
  if (renderQueued) return;
  renderQueued = true;
  requestAnimationFrame(() => {
    renderQueued = false;
    renderList();
  });
}

export function updateSizer() {
  ui.sizer.style.height = `${state.visible.length * ROW_HEIGHT}px`;
}

// only the rows in view exist in the DOM
export function renderList() {
  const viewport = ui.viewport;
  updateSizer();
  if (state.follow) viewport.scrollTop = followEdge() === "top" ? 0 : viewport.scrollHeight;

  const first = Math.max(0, Math.floor(viewport.scrollTop / ROW_HEIGHT) - OVERSCAN);
  const last = Math.min(
    state.visible.length,
    Math.ceil((viewport.scrollTop + viewport.clientHeight) / ROW_HEIGHT) + OVERSCAN,
  );

  let html = "";
  for (let position = first; position < last; position++) {
    const row = state.visible[position];
    row.html ??= rowHtml(row);
    html += `<div class="row${row === state.selected ? " selected" : ""}" data-position="${position}">${row.html}</div>`;
  }
  ui.rows.style.transform = `translateY(${first * ROW_HEIGHT}px)`;
  ui.rows.innerHTML = html;

  renderEmpty();
  renderStats();
  updateJumpPill();
}

export function rowHtml(row) {
  const { record, proto } = row;
  const saved = state.saved.has(record.id);
  const proxy = row.via ? `<span class="proxy-tag" title="Proxied through ${escapeHtml(row.via)}">proxy</span>` : "";
  const part = record.protos.length > 1 ? ` · ${row.index + 1}/${record.protos.length}` : "";
  return (
    `<button class="save${saved ? " on" : ""}" type="button" tabindex="-1" aria-label="${saved ? "Unsave" : "Save"}" ` +
    `title="${saved ? "Stop keeping this request" : "Save this request so the log keeps it (b)"}">` +
    '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6.5 3.5h11a1 1 0 0 1 1 1V21l-6.5-4.5L5.5 21V4.5a1 1 0 0 1 1-1Z"/></svg>' +
    "</button>" +
    `<span class="time" title="${formatFullTime(record.time)}">${timeCell(row)}</span>` +
    `<span class="method" title="${escapeHtml(row.method)}"><span class="name">${escapeHtml(row.method)}</span>` +
    `<span class="value">${row.value}</span>${proxy}</span>` +
    `<span class="status">${row.badge ? badgeHtml(row.badge) : ""}</span>` +
    `<span class="size" title="Request ${formatBytes(proto.request.size)}, response ${formatBytes(proto.response.size)}">` +
    `${formatBytes(proto.response.size)}</span>` +
    `<span class="rpc" title="RPC ID ${record.rpc_id}">#${record.rpc_id}${part}</span>` +
    `<span class="preview"><span class="req">${escapeHtml(row.preview[0])}</span><span class="arrow">→</span>` +
    `<span class="res">${escapeHtml(row.preview[1])}</span></span>`
  );
}

export function timeCell(row) {
  if (prefs.timeMode !== "delta") return row.time;
  return row.record.delta == null ? "—" : formatDelta(row.record.delta);
}

let emptyKey;
export function renderEmpty() {
  let key = null;
  if (!state.rows.length) key = !state.connected ? "offline" : state.paused ? "paused" : `waiting ${state.receiver}`;
  else if (!state.visible.length) key = "filtered";
  if (key === emptyKey) return;
  emptyKey = key;

  ui.logEmpty.hidden = key === null;
  if (key === "offline") {
    ui.logEmpty.innerHTML =
      '<div class="title">Connecting to Traffic Light…</div><div>Make sure it is still running</div>';
  } else if (key === "paused") {
    ui.logEmpty.innerHTML =
      '<div class="title">Capturing is paused</div><div>Incoming requests are ignored until you resume</div>' +
      '<button class="btn" type="button" data-action="resume">Resume</button>';
  } else if (key === "filtered") {
    ui.logEmpty.innerHTML =
      '<div class="title">Nothing matches your filters</div>' +
      '<button class="btn" type="button" data-action="reset-filters">Reset filters</button>';
  } else if (key !== null) {
    ui.logEmpty.innerHTML =
      '<img class="logo" src="static/logo.png" alt="" width="56" height="56"><div class="title">Waiting for traffic…</div>' +
      `<div>Set your MITM's POST destination to <code>${escapeHtml(state.receiver)}</code></div>`;
  }
}

export function renderStats() {
  const total = state.rows.length;
  const shown = state.visible.length;
  const count = (n, word) => `<b>${numberFormat.format(n)}</b> ${word}${n === 1 ? "" : "s"}`;
  const protos = shown === total ? count(total, "proto") : `<b>${numberFormat.format(shown)}</b> of ${count(total, "proto")}`;
  ui.stats.innerHTML = `${protos}<span class="sep">·</span>${count(state.records.length, "request")}`;
}

export function updateNewPill() {
  const show = !state.follow && state.unseen > 0;
  const top = followEdge() === "top";
  ui.newPill.hidden = !show;
  ui.logPills.classList.toggle("top", top);
  if (show) ui.newPill.textContent = `${top ? "↑" : "↓"} ${plural(state.unseen, "new proto")}`;
}

// the proto you are inspecting can scroll out of sight. This is the way back to it
export const updateJumpPill = debounce(() => {
  const position = state.selected ? state.visible.indexOf(state.selected) : -1;
  // the row carries the class only while it is inside the rendered window
  const away = position !== -1 && !ui.rows.querySelector(".row.selected");
  ui.jumpPill.hidden = !away;
  if (away) {
    const up = position * ROW_HEIGHT < ui.viewport.scrollTop;
    ui.jumpPill.textContent = `${up ? "↑" : "↓"} Selected proto`;
  }
}, 80);

export function jumpToSelected() {
  if (!state.selected) {
    toast("No proto is selected");
    return;
  }
  const position = state.visible.indexOf(state.selected);
  if (position === -1) {
    toast(state.selected.dropped ? "That proto is no longer in the log" : "That proto is hidden by the filters");
    return;
  }
  setFollow(false);
  center(position);
  scheduleRender();
}

export function setFollow(follow) {
  // sorted by anything but time, new rows show up all over the log
  follow = follow && followEdge() !== null;
  state.follow = follow;
  ui.follow.checked = follow;
  if (follow) {
    state.unseen = 0;
    // rendering scrolls to the newest rows while following
    scheduleRender();
  }
  updateNewPill();
}

export function select(row, mode = "preview") {
  if (!row) return;
  state.selected = row;
  scheduleRender();
  openInspect(row, mode);
}

export function moveTo(position) {
  const count = state.visible.length;
  if (!count) return;

  const target = clamp(position, 0, count - 1);
  // looking at older rows stops following, going to the newest one keeps it as it is
  if (target !== edgePosition()) setFollow(false);
  select(state.visible[target]);
  reveal(target);
}

export function moveSelection(delta) {
  const count = state.visible.length;
  let position = state.selected ? state.visible.indexOf(state.selected) : -1;
  if (position === -1) {
    // filtered out or dropped: carry on from where it would sit instead of jumping to the end
    position = state.selected
      ? insertionPoint(state.selected) - (delta > 0 ? 1 : 0)
      : delta > 0
        ? -1
        : count;
  }
  moveTo(position + delta);
}

export function reveal(position) {
  const viewport = ui.viewport;
  const top = position * ROW_HEIGHT;
  if (top < viewport.scrollTop) viewport.scrollTop = top;
  else if (top + ROW_HEIGHT > viewport.scrollTop + viewport.clientHeight) {
    viewport.scrollTop = top + ROW_HEIGHT - viewport.clientHeight;
  }
}

export function center(position) {
  ui.viewport.scrollTop = position * ROW_HEIGHT - (ui.viewport.clientHeight - ROW_HEIGHT) / 2;
}

export const pageSize = () => Math.max(1, Math.floor(ui.viewport.clientHeight / ROW_HEIGHT) - 1);

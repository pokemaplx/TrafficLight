"use strict";

(() => {
  // ------------------------------------------------------------------ constants

  const ROW_HEIGHT = 46; // keep in sync with --row-h in style.css
  const OVERSCAN = 10;
  const PREVIEW_LENGTH = 300;
  const DETAIL_CACHE_SIZE = 30;
  const SMALL_MESSAGE = 250; // messages with up to this many values are expanded completely
  const CODE_LIMIT = 1_000_000; // characters the JSON and Text views render before truncating
  const STORAGE_KEY = "trafficlight";
  const QUERY_KEYS = { method: "method", m: "method", msg: "msg", message: "msg", status: "status", s: "status" };
  const TABS = ["tree", "json", "text"];
  // the columns the log can be sorted by, and whether a column sorts descending when it's picked
  const SORT_FIRST_DESC = { time: false, method: false, status: false, size: true };
  const THEMES = ["system", "light", "dark"];
  const THEME_NAMES = { system: "System theme", light: "Light theme", dark: "Dark theme" };

  // [regex, css class per capture group]
  const JSON_SYNTAX = [
    /("(?:[^"\\]|\\.)*")(?=\s*:)|("(?:[^"\\]|\\.)*")|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false|null)\b|([{}[\],])/g,
    ["k", "s", "n", "b", "p"],
  ];
  const TEXT_SYNTAX = [
    /("(?:[^"\\]|\\.)*")|([A-Za-z_]\w*)(?=:| \{)|(-?\d[\w.+-]*)|\b(true|false)\b|([A-Za-z_]\w*)|([{}])/g,
    ["s", "k", "n", "b", "e", "p"],
  ];

  // ------------------------------------------------------------------ helpers

  const $ = (id) => document.getElementById(id);
  const numberFormat = new Intl.NumberFormat();
  const plural = (count, word) => `${numberFormat.format(count)} ${word}${count === 1 ? "" : "s"}`;
  const HTML_ESCAPES = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };
  const escapeHtml = (value) => String(value).replace(/[&<>"']/g, (char) => HTML_ESCAPES[char]);
  const escapeRegExp = (value) => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const clamp = (value, min, max) => Math.min(Math.max(value, min), max);

  function formatBytes(bytes) {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10240 ? 1 : 0)} KB`;
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  }

  function formatTime(ms) {
    const date = new Date(ms);
    const pad = (value, length = 2) => String(value).padStart(length, "0");
    return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`;
  }

  function debounce(fn, wait) {
    let timer = null;
    const run = () => {
      clearTimeout(timer);
      timer = null;
      fn();
    };
    const debounced = () => {
      clearTimeout(timer);
      timer = setTimeout(run, wait);
    };
    debounced.flush = () => {
      if (timer !== null) run();
    };
    return debounced;
  }

  // ------------------------------------------------------------------ elements, preferences and state

  const ui = {
    search: $("search"),
    searchClear: $("search-clear"),
    suggestions: $("suggestions"),
    helpBtn: $("help-btn"),
    methodsBtn: $("methods-btn"),
    methodsBadge: $("methods-badge"),
    firstOnly: $("first-only"),
    pauseBtn: $("pause-btn"),
    follow: $("follow"),
    clearBtn: $("clear-btn"),
    themeBtn: $("theme-btn"),
    exportBtn: $("export-btn"),
    main: $("main"),
    viewport: $("viewport"),
    sizer: $("sizer"),
    rows: $("rows"),
    logHead: $("log-head"),
    logEmpty: $("log-empty"),
    newPill: $("new-pill"),
    splitter: $("splitter"),
    inspectorEmpty: $("inspector-empty"),
    inspectorHead: $("inspector-head"),
    inspectorTools: $("inspector-tools"),
    inspectorBody: $("inspector-body"),
    tabs: $("tabs"),
    treeTools: $("tree-tools"),
    expandBtn: $("expand-btn"),
    collapseBtn: $("collapse-btn"),
    typesToggle: $("types-toggle"),
    copyJsonBtn: $("copy-json-btn"),
    copyTextBtn: $("copy-text-btn"),
    conn: $("conn"),
    receiver: $("receiver"),
    receiverUrl: $("receiver-url"),
    shortcutsBtn: $("shortcuts-btn"),
    logoutBtn: $("logout-btn"),
    pausedNote: $("paused-note"),
    stats: $("stats"),
    methodsPop: $("methods-pop"),
    methodsSearch: $("methods-search"),
    methodsAll: $("methods-all"),
    methodsNone: $("methods-none"),
    methodsSummary: $("methods-summary"),
    methodsList: $("methods-list"),
    helpPop: $("help-pop"),
    toast: $("toast"),
  };

  const prefs = {
    theme: "system",
    query: "",
    firstOnly: false,
    tab: "tree",
    types: true,
    listWidth: null,
    methods: { mode: "exclude", list: [] },
    sort: { key: "time", desc: false },
    ...readPrefs(),
  };
  if (!(prefs.sort?.key in SORT_FIRST_DESC) || typeof prefs.sort.desc !== "boolean") {
    prefs.sort = { key: "time", desc: false };
  }
  if (!THEMES.includes(prefs.theme)) prefs.theme = "system";
  if (!TABS.includes(prefs.tab)) prefs.tab = "tree";
  if (typeof prefs.query !== "string") prefs.query = "";

  function readPrefs() {
    try {
      const stored = JSON.parse(localStorage.getItem(STORAGE_KEY));
      return stored && typeof stored === "object" ? stored : {};
    } catch {
      return {};
    }
  }

  function savePrefs() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
    } catch {
      // storage can be unavailable, preferences just won't stick
    }
  }

  const state = {
    session: null,
    connected: false,
    paused: false,
    receiver: "",
    maxRecords: Infinity,
    records: [],
    rows: [], // one per proto, oldest first
    visible: [], // rows that pass the filters
    selected: null, // row highlighted in the log
    inspected: null, // row shown in the inspector
    restoreKey: null, // selection to restore after reconnecting
    restoreFollow: true, // whether the log followed before reconnecting
    follow: true,
    unseen: 0,
    query: parseQuery(prefs.query),
    methodFilter: {
      mode: prefs.methods?.mode === "include" ? "include" : "exclude",
      set: new Set(Array.isArray(prefs.methods?.list) ? prefs.methods.list : []),
    },
    methodCounts: new Map(),
    messageNames: new Set(),
    statuses: new Set(),
    detail: null,
    renderedDetail: null,
    sectionState: new Map(),
  };

  // ------------------------------------------------------------------ filter query

  // splits a query into tokens: [-][key:](word | "phrase" | /regex/)
  function tokenize(input) {
    const tokens = [];
    let i = 0;
    while (i < input.length) {
      if (/\s/.test(input[i])) {
        i++;
        continue;
      }

      const token = { start: i, negate: false, key: null, value: "", regex: null };
      if (input[i] === "-" && i + 1 < input.length && !/\s/.test(input[i + 1])) {
        token.negate = true;
        i++;
      }
      const key = /^([a-z]+):/i.exec(input.slice(i, i + 10));
      if (key && QUERY_KEYS[key[1].toLowerCase()]) {
        token.key = QUERY_KEYS[key[1].toLowerCase()];
        i += key[0].length;
      }
      token.valueStart = i;

      if (input[i] === '"') {
        const end = input.indexOf('"', i + 1);
        token.value = input.slice(i + 1, end === -1 ? input.length : end);
        i = end === -1 ? input.length : end + 1;
      } else if (input[i] === "/") {
        let end = i + 1;
        while (end < input.length && input[end] !== "/") end += input[end] === "\\" ? 2 : 1;
        token.value = token.regex = input.slice(i + 1, Math.min(end, input.length));
        i = Math.min(end + 1, input.length);
      } else {
        while (i < input.length && !/\s/.test(input[i])) i++;
        token.value = input.slice(token.valueStart, i);
      }

      token.end = i;
      tokens.push(token);
    }
    return tokens;
  }

  function parseQuery(input) {
    const terms = [];
    let error = null;
    for (const token of tokenize(input)) {
      if (!token.value) continue;
      const source = token.regex ?? escapeRegExp(token.value);
      try {
        const test = new RegExp(source, "i");
        terms.push({
          key: token.key,
          negate: token.negate,
          value: token.value.toLowerCase(),
          regex: token.regex === null ? null : test,
          test,
          global: new RegExp(source, "gi"),
        });
      } catch (e) {
        error = e.message;
      }
    }
    return { terms, error };
  }

  function matches(row) {
    if (prefs.firstOnly && row.index > 0) return false;
    if (!methodShown(row.method)) return false;

    for (const term of state.query.terms) {
      const haystack =
        term.key === "method"
          ? row.methodText
          : term.key === "msg"
            ? row.messageText
            : term.key === "status"
              ? row.statusText
              : row.searchText;
      const found = term.regex ? term.regex.test(haystack) : haystack.includes(term.value);
      if (found === term.negate) return false;
    }
    return true;
  }

  function methodShown(method) {
    const { mode, set } = state.methodFilter;
    return set.has(method) === (mode === "include");
  }

  // terms worth highlighting in the inspector
  function highlightTerms() {
    return state.query.terms.filter((term) => !term.negate && !term.key);
  }

  // ------------------------------------------------------------------ records

  let rowSequence = 0; // arrival order

  function makeRow(record, proto, index) {
    const inner = proto.proxy || proto;
    const method = inner.method || `UNKNOWN_${inner.value}`;
    const messages = proto.proxy
      ? [proto.request, proto.response, inner.request, inner.response]
      : [proto.request, proto.response];
    const names = messages.map((message) => message.name).filter(Boolean);
    const badge = rowBadge(proto, inner);
    // what the row shows has to be searchable too, i.e. UNKNOWN_123 or "decode error"
    const statusText = [proto.status, badge?.text].filter(Boolean).join(" ").toLowerCase();

    names.forEach((name) => state.messageNames.add(name));
    if (proto.status) state.statuses.add(proto.status);
    state.methodCounts.set(method, (state.methodCounts.get(method) || 0) + 1);

    const row = {
      key: `${record.id}:${index}`,
      seq: rowSequence++,
      record,
      index,
      proto,
      method,
      value: inner.value,
      via: proto.proxy ? `${proto.method || "UNKNOWN"} ${proto.value}` : null,
      badge,
      time: formatTime(record.time),
      preview: [previewOf(inner.request), previewOf(inner.response)],
      methodText: [method, proto.method, proto.value, proto.proxy?.value]
        .filter((part) => part != null)
        .join(" ")
        .toLowerCase(),
      messageText: names.join(" ").toLowerCase(),
      statusText,
      searchText: [method, proto.method, ...names, statusText, ...messages.map((message) => message.text)]
        .filter(Boolean)
        .join("\n")
        .toLowerCase(),
      html: null,
    };

    // the inspector loads complete messages on its own, only the search text is needed from here on
    messages.forEach((message) => {
      message.text = undefined;
    });
    return row;
  }

  function previewOf(message) {
    return message.text ? message.text.slice(0, PREVIEW_LENGTH) : "{}";
  }

  function rowBadge(proto, inner) {
    const states = [inner.request.state, inner.response.state];
    if (states.includes("error")) return { text: "decode error", tone: "bad" };
    if (states.includes("unknown")) return { text: "unknown", tone: "warn" };
    if (proto.status) return { text: proto.status, tone: statusTone(proto.status) };
    if (inner.response.state === "empty") return { text: "empty", tone: "muted" };
    return null;
  }

  function statusTone(status) {
    if (/SUCCESS|COMPLETED|(^|_)OK$/.test(status)) return "ok";
    if (/ERROR|FAIL|INVALID|DENIED|UNAUTHORIZED|NOT_FOUND|TIMEOUT|RATE_LIMITED|BANNED/.test(status)) return "bad";
    return "";
  }

  // history is what the server already had when connecting, none of it is new
  function addRecords(records, history = false) {
    const added = [];
    for (const record of records) {
      record.rows = record.protos.map((proto, index) => makeRow(record, proto, index));
      state.records.push(record);
      for (const row of record.rows) {
        state.rows.push(row);
        added.push(row);
      }
    }
    let matching = [];
    keepInView(() => {
      trimRecords();
      matching = added.filter((row) => !row.dropped && matches(row));
      insertVisible(matching);
    });
    if (
      !history &&
      !state.follow &&
      followEdge() &&
      matching.length &&
      state.visible.length * ROW_HEIGHT > ui.viewport.clientHeight
    ) {
      state.unseen += matching.length;
    }

    if (state.restoreKey) {
      const row = added.find((candidate) => candidate.key === state.restoreKey);
      if (row) {
        state.restoreKey = null;
        state.selected = row;
        const position = state.visible.indexOf(row);
        // the log is empty for a moment while reconnecting, which looks like being scrolled to the bottom
        if (!state.restoreFollow && position !== -1) {
          setFollow(false);
          updateSizer();
          center(position);
        }
      }
    }

    updateNewPill();
    scheduleRender();
    scheduleMethods();
  }

  // the server only keeps the newest records, so does the browser
  function trimRecords() {
    const excess = state.records.length - state.maxRecords;
    if (excess <= 0) return;

    let droppedRows = 0;
    for (const record of state.records.splice(0, excess)) {
      for (const row of record.rows) {
        row.dropped = true;
        droppedRows++;
        const count = state.methodCounts.get(row.method) - 1;
        if (count > 0) state.methodCounts.set(row.method, count);
        else state.methodCounts.delete(row.method);
      }
    }
    state.rows.splice(0, droppedRows);
    // depending on the sorting, dropped rows can be anywhere in the log
    state.visible = state.visible.filter((row) => !row.dropped);
  }

  function resetRecords() {
    state.records = [];
    state.rows = [];
    state.visible = [];
    state.methodCounts = new Map();
    state.selected = null;
    state.unseen = 0;
    updateNewPill();
    scheduleRender();
    scheduleMethods();
  }

  function refilter() {
    state.visible = state.rows.filter(matches);
    if (!sortedByArrival()) state.visible.sort(compareRows);
    state.unseen = 0;
    updateNewPill();
    updateSizer();

    // keep the selection in sight
    if (!state.follow && state.selected) {
      const position = state.visible.indexOf(state.selected);
      if (position !== -1) center(position);
    }
    scheduleRender();
  }

  // ------------------------------------------------------------------ sorting

  const compareText = (a, b) => (a < b ? -1 : a > b ? 1 : 0);
  const SORT_COMPARE = {
    time: (a, b) => a.seq - b.seq,
    method: (a, b) => compareText(a.method, b.method),
    status: (a, b) => compareText(a.badge?.text ?? "", b.badge?.text ?? ""),
    size: (a, b) => a.proto.response.size - b.proto.response.size,
  };

  function compareRows(a, b) {
    const { key, desc } = prefs.sort;
    // rows without a status go last either way
    if (key === "status" && !a.badge !== !b.badge) return a.badge ? -1 : 1;
    const result = SORT_COMPARE[key](a, b);
    return (desc ? -result : result) || a.seq - b.seq;
  }

  const sortedByArrival = () => prefs.sort.key === "time" && !prefs.sort.desc;

  // where new rows show up, so where following sticks to. Other columns put them anywhere
  function followEdge() {
    if (prefs.sort.key !== "time") return null;
    return prefs.sort.desc ? "top" : "bottom";
  }

  function edgePosition() {
    const edge = followEdge();
    return edge === "top" ? 0 : edge === "bottom" ? state.visible.length - 1 : -1;
  }

  function insertVisible(rows) {
    if (sortedByArrival()) {
      for (const row of rows) state.visible.push(row);
    } else if (rows.length > 64) {
      for (const row of rows) state.visible.push(row);
      state.visible.sort(compareRows);
    } else {
      for (const row of rows) state.visible.splice(insertionPoint(row), 0, row);
    }
  }

  function insertionPoint(row) {
    let low = 0;
    let high = state.visible.length;
    while (low < high) {
      const middle = (low + high) >> 1;
      if (compareRows(state.visible[middle], row) <= 0) low = middle + 1;
      else high = middle;
    }
    return low;
  }

  // keeps the rows in view where they are while rows are added or removed above them
  function keepInView(change) {
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

  function setSort(key) {
    prefs.sort = { key, desc: key === prefs.sort.key ? !prefs.sort.desc : SORT_FIRST_DESC[key] };
    savePrefs();
    updateSortUi();

    // show the selection, otherwise the newest rows, otherwise the top
    const edge = followEdge();
    setFollow(!state.selected && edge !== null);
    refilter();
    if (!state.selected && !edge) ui.viewport.scrollTop = 0;
  }

  function updateSortUi() {
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

  // ------------------------------------------------------------------ log

  let renderQueued = false;
  function scheduleRender() {
    if (renderQueued) return;
    renderQueued = true;
    requestAnimationFrame(() => {
      renderQueued = false;
      renderList();
    });
  }

  function updateSizer() {
    ui.sizer.style.height = `${state.visible.length * ROW_HEIGHT}px`;
  }

  // only the rows in view exist in the DOM
  function renderList() {
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
  }

  function rowHtml(row) {
    const { record, proto } = row;
    const proxy = row.via ? `<span class="proxy-tag" title="Proxied through ${escapeHtml(row.via)}">proxy</span>` : "";
    const part = record.protos.length > 1 ? ` · ${row.index + 1}/${record.protos.length}` : "";
    return (
      `<span class="time">${row.time}</span>` +
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

  function badgeHtml(badge) {
    return `<span class="status-badge ${badge.tone}" title="${escapeHtml(badge.text)}">${escapeHtml(badge.text)}</span>`;
  }

  let emptyKey;
  function renderEmpty() {
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

  function renderStats() {
    const total = state.rows.length;
    const shown = state.visible.length;
    const count = (n, word) => `<b>${numberFormat.format(n)}</b> ${word}${n === 1 ? "" : "s"}`;
    const protos = shown === total ? count(total, "proto") : `<b>${numberFormat.format(shown)}</b> of ${count(total, "proto")}`;
    ui.stats.innerHTML = `${protos}<span class="sep">·</span>${count(state.records.length, "request")}`;
  }

  function updateNewPill() {
    const show = !state.follow && state.unseen > 0;
    const top = followEdge() === "top";
    ui.newPill.hidden = !show;
    ui.newPill.classList.toggle("top", top);
    if (show) ui.newPill.textContent = `${top ? "↑" : "↓"} ${plural(state.unseen, "new proto")}`;
  }

  function setFollow(follow) {
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

  // ------------------------------------------------------------------ selection

  function select(row) {
    if (!row || row === state.selected) return;
    state.selected = row;
    scheduleRender();
    inspect(row);
  }

  function moveTo(position) {
    const count = state.visible.length;
    if (!count) return;

    const target = clamp(position, 0, count - 1);
    // looking at older rows stops following, going to the newest one keeps it as it is
    if (target !== edgePosition()) setFollow(false);
    select(state.visible[target]);
    reveal(target);
  }

  function moveSelection(delta) {
    const count = state.visible.length;
    let position = state.selected ? state.visible.indexOf(state.selected) : -1;
    if (position === -1) position = delta > 0 ? -1 : count;
    moveTo(position + delta);
  }

  function reveal(position) {
    const viewport = ui.viewport;
    const top = position * ROW_HEIGHT;
    if (top < viewport.scrollTop) viewport.scrollTop = top;
    else if (top + ROW_HEIGHT > viewport.scrollTop + viewport.clientHeight) {
      viewport.scrollTop = top + ROW_HEIGHT - viewport.clientHeight;
    }
  }

  function center(position) {
    ui.viewport.scrollTop = position * ROW_HEIGHT - (ui.viewport.clientHeight - ROW_HEIGHT) / 2;
  }

  const pageSize = () => Math.max(1, Math.floor(ui.viewport.clientHeight / ROW_HEIGHT) - 1);

  // ------------------------------------------------------------------ inspector

  const detailCache = new Map();
  let detailRequest = null;

  async function inspect(row) {
    state.inspected = row;
    state.detail = null;
    state.sectionState = new Map();
    renderInspectorHead(row);
    ui.inspectorEmpty.hidden = true;
    ui.inspectorHead.hidden = false;
    ui.inspectorTools.hidden = false;

    detailRequest?.abort();
    detailRequest = null;

    const cached = detailCache.get(row.key);
    if (cached) {
      showDetail(cached);
      return;
    }

    const controller = new AbortController();
    detailRequest = controller;
    ui.inspectorBody.classList.add("loading");
    try {
      const response = await fetch(`api/records/${row.record.id}/${row.index}`, { signal: controller.signal });
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
      detailCache.set(row.key, detail);
      if (detailCache.size > DETAIL_CACHE_SIZE) detailCache.delete(detailCache.keys().next().value);
      if (detailRequest === controller) showDetail(detail);
    } catch (error) {
      // another proto was selected in the meantime
      if (detailRequest !== controller) return;
      ui.inspectorBody.innerHTML = `<div class="inspector-message">${escapeHtml(error.message)}</div>`;
    } finally {
      if (detailRequest === controller) {
        detailRequest = null;
        ui.inspectorBody.classList.remove("loading");
      }
    }
  }

  function showDetail(detail) {
    state.detail = detail;
    ui.inspectorBody.classList.remove("loading");
    renderInspectorBody();
  }

  function clearInspector() {
    detailRequest?.abort();
    detailRequest = null;
    state.inspected = null;
    state.detail = null;
    state.renderedDetail = null;
    ui.inspectorEmpty.hidden = false;
    ui.inspectorHead.hidden = true;
    ui.inspectorTools.hidden = true;
    ui.inspectorBody.replaceChildren();
    ui.inspectorBody.classList.remove("loading");
  }

  function renderInspectorHead(row) {
    const { record } = row;
    const meta = [row.time, `RPC ID ${record.rpc_id}`, `RPC status ${record.rpc_status}`];
    if (record.rpc_handle != null) meta.push(`RPC handle ${record.rpc_handle}`);
    if (record.protos.length > 1) meta.push(`proto ${row.index + 1} of ${record.protos.length}`);

    ui.inspectorHead.innerHTML =
      '<div class="ih-title">' +
      `<span class="ih-method">${escapeHtml(row.method)}</span>` +
      `<span class="ih-value">${row.value}</span>` +
      (row.badge ? badgeHtml(row.badge) : "") +
      '<span class="ih-actions">' +
      '<button class="mini" type="button" data-action="hide-method" title="Hide this method (h)">Hide method</button>' +
      '<button class="mini" type="button" data-action="only-method" title="Only show this method">Only this method</button>' +
      "</span></div>" +
      (row.via ? `<div class="ih-via">via ${escapeHtml(row.via)}</div>` : "") +
      `<div class="ih-meta">${meta.map(escapeHtml).join('<span class="sep">·</span>')}</div>`;
  }

  function renderInspectorBody() {
    const detail = state.detail;
    if (!detail) return;

    const terms = highlightTerms();
    const context = { terms, matcher: makeMatcher(terms) };
    const inner = detail.proxy || detail;
    const sections = [
      makeSection("Request", inner.request, true, context),
      makeSection("Response", inner.response, true, context),
    ];
    if (detail.proxy) {
      sections.push(
        makeSection("Proxy request", detail.request, false, context),
        makeSection("Proxy response", detail.response, false, context),
      );
    }

    const body = ui.inspectorBody;
    const sameDetail = state.renderedDetail === detail;
    const scrollTop = body.scrollTop;
    body.classList.toggle("no-types", !prefs.types);
    body.replaceChildren(...sections);
    state.renderedDetail = detail;

    const mark = terms.length ? body.querySelector("mark") : null;
    if (mark) mark.scrollIntoView({ block: "center" });
    else body.scrollTop = sameDetail ? scrollTop : 0;
  }

  function makeSection(name, message, openByDefault, context) {
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
    setSectionOpen(section, state.sectionState.get(name) ?? openByDefault);
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

  function setSectionOpen(section, open) {
    if (open && !section._rendered) {
      section._rendered = true;
      renderSectionBody(section.lastElementChild, section._message, section._context);
    }
    section.classList.toggle("open", open);
    state.sectionState.set(section._name, open);
  }

  function renderSectionBody(body, message, context) {
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

    if (prefs.tab === "tree") {
      body.append(renderTree(message.tree || jsonToNode(message.json), context));
      return;
    }

    const blackbox = !message.tree;
    const source = prefs.tab === "json" ? JSON.stringify(message.json ?? null, null, 2) : message.text;
    const truncated = source.length > CODE_LIMIT;
    const [regex, kinds] = prefs.tab === "json" || blackbox ? JSON_SYNTAX : TEXT_SYNTAX;
    body.insertAdjacentHTML(
      "beforeend",
      `<pre class="code">${highlightCode(truncated ? source.slice(0, CODE_LIMIT) : source, regex, kinds, context.terms)}</pre>` +
        (truncated ? '<div class="note-box">Too long to show completely. The copy buttons copy everything.</div>' : ""),
    );
  }

  // ------------------------------------------------------------------ tree

  // nodes come from the server (see serialize.py) or from jsonToNode for messages without a definition
  function renderTree(root, context) {
    const tree = document.createElement("div");
    tree.className = "tree";
    appendFields(tree, root, 1, { ...context, openDepth: isSmall(root) ? Infinity : 1 });
    return tree;
  }

  function isSmall(message) {
    let budget = SMALL_MESSAGE;
    const visit = (node) => {
      for (const [, , repeated, value] of node.f) {
        for (const item of repeated ? value : [value]) {
          if (--budget < 0) return false;
          if (item.k === "msg" && !visit(item)) return false;
        }
      }
      return true;
    };
    return visit(message);
  }

  function appendFields(container, message, depth, context) {
    if (!message.f.length) {
      container.insertAdjacentHTML("beforeend", '<div class="tl"><span class="tw"></span><span class="v null">{}</span></div>');
      return;
    }

    for (const [name, type, repeated, value] of message.f) {
      const isMessage = repeated ? value[0]?.k === "msg" : value.k === "msg";
      const label = fieldLabel(name, repeated && type ? `${type}[]` : type, isMessage, context);
      if (repeated) container.append(listNode(label, value, depth, context));
      else if (value.k === "msg") container.append(messageNode(label, value, depth, context));
      else container.append(scalarLine(label, value, context));
    }
  }

  function fieldLabel(name, type, isMessage, context) {
    const typeHtml = type ? `<span class="ty${isMessage ? " msg" : ""}">${escapeHtml(type)}</span>` : "";
    return `${typeHtml}<span class="k">${highlight(name, context.terms)}</span>`;
  }

  function scalarLine(label, value, context) {
    const line = document.createElement("div");
    line.className = "tl";
    line.innerHTML = `<span class="tw"></span>${label}<span class="c">:</span>${valueHtml(value, context.terms)}`;
    return line;
  }

  function messageNode(label, message, depth, context) {
    const open = depth <= context.openDepth || Boolean(context.matcher?.message(message));
    return collapsible(label, messagePreview(message), open, (children) =>
      appendFields(children, message, depth + 1, context),
    );
  }

  function listNode(label, items, depth, context) {
    const scalars = items.length > 0 && items[0].k !== "msg";
    const preview = scalars
      ? escapeHtml(items.slice(0, 10).map(shortValue).join(", ") + (items.length > 10 ? ", …" : ""))
      : "";
    // long lists of numbers read better as the one line preview
    const open =
      (depth <= context.openDepth && items.length <= (scalars ? 10 : 100)) ||
      Boolean(context.matcher && items.some(context.matcher.value));
    return collapsible(`${label}<span class="count">[${items.length}]</span>`, preview, open, (children) => {
      items.forEach((item, index) => {
        const itemLabel = `<span class="k idx">[${index}]</span>`;
        children.append(
          item.k === "msg" ? messageNode(itemLabel, item, depth + 1, context) : scalarLine(itemLabel, item, context),
        );
      });
    });
  }

  // children are only built when a node is opened for the first time
  function collapsible(label, preview, open, build) {
    const node = document.createElement("div");
    node.className = "tn";
    node.innerHTML = `<div class="tl x"><span class="tw"></span>${label}<span class="pv">${preview}</span></div><div class="tc"></div>`;
    node._build = build;
    if (open) setNodeOpen(node, true);
    return node;
  }

  function setNodeOpen(node, open) {
    if (open && node._build) {
      const build = node._build;
      node._build = null;
      build(node.lastElementChild);
    }
    node.classList.toggle("open", open);
  }

  function setNodeOpenDeep(node, open) {
    setNodeOpen(node, open);
    for (const child of node.lastElementChild.children) {
      if (child.classList.contains("tn")) setNodeOpenDeep(child, open);
    }
  }

  function setTreeOpen(open) {
    for (const section of ui.inspectorBody.querySelectorAll(".section")) {
      if (open) setSectionOpen(section, true);
      for (const node of section.querySelectorAll(".tree > .tn")) setNodeOpenDeep(node, open);
    }
  }

  function valueHtml(value, terms) {
    const text = (content) => highlight(content, terms);
    switch (value.k) {
      case "str":
        return `<span class="v str">"${text(value.v)}"</span>`;
      case "bytes":
        return `<span class="v str">"${text(value.v)}"</span><span class="note">${plural(value.n, "byte")}</span>`;
      case "enum":
        return value.e == null
          ? `<span class="v enum">${text(value.v)}</span>`
          : `<span class="v enum">${text(value.e)}</span><span class="note">${value.v}</span>`;
      case "bool":
        return `<span class="v bool">${value.v}</span>`;
      case "null":
        return '<span class="v null">null</span>';
      default:
        return `<span class="v num">${text(value.v)}</span>`;
    }
  }

  function shortValue(value) {
    switch (value.k) {
      case "str":
      case "bytes":
        return JSON.stringify(value.v.length > 32 ? `${value.v.slice(0, 32)}…` : value.v);
      case "enum":
        return value.e ?? String(value.v);
      case "msg":
        return "{…}";
      default:
        return String(value.v);
    }
  }

  function messagePreview(message) {
    const parts = [];
    let length = 0;
    for (const [name, , repeated, value] of message.f) {
      if (length > 100) {
        parts.push("…");
        break;
      }
      const part = `${name}: ${repeated ? `[${value.length}]` : shortValue(value)}`;
      parts.push(part);
      length += part.length + 2;
    }
    return escapeHtml(parts.length ? `{ ${parts.join(", ")} }` : "{}");
  }

  // tells which nodes contain a search match, so they can be opened
  function makeMatcher(terms) {
    if (!terms.length) return null;

    const test = (text) => terms.some((term) => term.test.test(text));
    const memo = new WeakMap();
    const value = (node) =>
      node.k === "msg" ? message(node) : test(node.k === "enum" ? `${node.e ?? ""} ${node.v}` : String(node.v));
    const message = (node) => {
      let found = memo.get(node);
      if (found === undefined) {
        found = node.f.some(([name, , repeated, child]) => test(name) || (repeated ? child.some(value) : value(child)));
        memo.set(node, found);
      }
      return found;
    };
    return { value, message };
  }

  function jsonToNode(value) {
    if (Array.isArray(value)) {
      return { k: "msg", t: "", f: value.map((item, index) => [String(index), "", false, jsonToNode(item)]) };
    }
    if (value !== null && typeof value === "object") {
      return {
        k: "msg",
        t: "",
        f: Object.entries(value).map(([key, item]) =>
          Array.isArray(item) ? [key, "", true, item.map(jsonToNode)] : [key, "", false, jsonToNode(item)],
        ),
      };
    }
    if (typeof value === "string") return { k: "str", v: value };
    if (typeof value === "boolean") return { k: "bool", v: value };
    if (value === null) return { k: "null", v: null };
    return { k: "num", v: value };
  }

  // ------------------------------------------------------------------ highlighting

  function highlight(text, terms) {
    text = String(text);
    if (!terms || !terms.length) return escapeHtml(text);

    const ranges = [];
    for (const term of terms) {
      const regex = term.global;
      regex.lastIndex = 0;
      let match;
      while ((match = regex.exec(text)) !== null) {
        if (match[0] === "") {
          regex.lastIndex++;
          continue;
        }
        ranges.push([match.index, match.index + match[0].length]);
      }
    }
    if (!ranges.length) return escapeHtml(text);

    ranges.sort((a, b) => a[0] - b[0]);
    let html = "";
    let position = 0;
    for (const [start, end] of ranges) {
      if (end <= position) continue;
      const from = Math.max(start, position);
      html += `${escapeHtml(text.slice(position, from))}<mark>${escapeHtml(text.slice(from, end))}</mark>`;
      position = end;
    }
    return html + escapeHtml(text.slice(position));
  }

  function highlightCode(source, regex, kinds, terms) {
    let html = "";
    let position = 0;
    for (const match of source.matchAll(regex)) {
      const kind = kinds[match.slice(1).findIndex((group) => group !== undefined)];
      html += highlight(source.slice(position, match.index), terms);
      html += `<span class="${kind}">${highlight(match[0], terms)}</span>`;
      position = match.index + match[0].length;
    }
    return html + highlight(source.slice(position), terms);
  }

  // ------------------------------------------------------------------ copying

  let toastTimer;
  function toast(message) {
    ui.toast.textContent = message;
    ui.toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
      ui.toast.hidden = true;
    }, 1800);
  }

  async function copy(text, what) {
    try {
      if (navigator.clipboard && window.isSecureContext) await navigator.clipboard.writeText(text);
      else legacyCopy(text);
      toast(`Copied ${what}`);
    } catch {
      toast("Couldn't copy to the clipboard");
    }
  }

  // the clipboard api only exists on secure origins, which a LAN address isn't
  function legacyCopy(text) {
    const area = document.createElement("textarea");
    area.value = text;
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.append(area);
    area.select();
    const copied = document.execCommand("copy");
    area.remove();
    if (!copied) throw new Error("copy failed");
  }

  function protoJson(detail) {
    const message = (m) => ({ name: m.name, data: m.json });
    return {
      method: detail.method,
      value: detail.value,
      request: message(detail.request),
      response: message(detail.response),
      ...(detail.proxy ? { proxy: protoJson(detail.proxy) } : {}),
    };
  }

  // same layout as copying in the terminal UI
  function protoText(detail) {
    const message = (m) => {
      const name = m.name || "Unknown message";
      if (!m.tree) return `${name} ${m.text}`;
      const body = m.text.trimEnd();
      return body ? `${name} {\n${body.replace(/^/gm, "  ")}\n}` : `${name} {}`;
    };
    let text = `${detail.method || "Unknown method"} | ${detail.value}\n\n${message(detail.request)}\n\n${message(detail.response)}`;
    if (detail.proxy) text += `\n\nProxy: ${protoText(detail.proxy)}`;
    return text;
  }

  function copyInspected(format) {
    if (!state.detail) return;
    if (format === "json") copy(JSON.stringify(protoJson(state.detail), null, 2), "proto as JSON");
    else copy(protoText(state.detail), "proto as text");
  }

  // ------------------------------------------------------------------ search box

  const applySearch = debounce(() => {
    prefs.query = ui.search.value;
    savePrefs();
    state.query = parseQuery(prefs.query);
    ui.search.classList.toggle("invalid", Boolean(state.query.error));
    ui.search.title = state.query.error ? `Invalid regular expression: ${state.query.error}` : "";
    refilter();
    renderInspectorBody();
  }, 150);

  function setSearch(value) {
    ui.search.value = value;
    ui.searchClear.hidden = !value;
    applySearch();
    applySearch.flush();
  }

  function focusSearch() {
    ui.search.focus();
    ui.search.select();
  }

  let suggestion = { items: [], active: 0, token: null };

  // completes values after method:, msg: and status:
  function updateSuggestions() {
    const input = ui.search;
    if (document.activeElement !== input) return hideSuggestions();

    const caret = input.selectionStart;
    const token = tokenize(input.value).find(
      (candidate) => candidate.key && candidate.regex === null && candidate.valueStart <= caret && caret <= candidate.end,
    );
    if (!token) return hideSuggestions();

    const query = token.value.toLowerCase();
    const counts = state.methodCounts;
    const source =
      token.key === "method" ? [...counts.keys()] : token.key === "msg" ? [...state.messageNames] : [...state.statuses];
    const rank = (item) => (item.toLowerCase().startsWith(query) ? 0 : 1);
    const items = source
      .filter((item) => item.toLowerCase().includes(query) && item.toLowerCase() !== query)
      .sort((a, b) => rank(a) - rank(b) || (counts.get(b) || 0) - (counts.get(a) || 0) || a.localeCompare(b))
      .slice(0, 12);
    if (!items.length) return hideSuggestions();

    suggestion = { items, active: 0, token };
    renderSuggestions();
  }

  function renderSuggestions() {
    const { items, active, token } = suggestion;
    const terms = token.value ? [{ global: new RegExp(escapeRegExp(token.value), "gi") }] : [];
    ui.suggestions.innerHTML = items
      .map((item, index) => {
        const hint = token.key === "method" ? numberFormat.format(state.methodCounts.get(item) || 0) : "";
        return (
          `<li role="option" data-index="${index}" class="${index === active ? "active" : ""}">` +
          `<span>${highlight(item, terms)}</span><span class="hint">${hint}</span></li>`
        );
      })
      .join("");
    ui.suggestions.hidden = false;
  }

  function moveSuggestion(delta) {
    const count = suggestion.items.length;
    suggestion.active = (suggestion.active + delta + count) % count;
    renderSuggestions();
    ui.suggestions.children[suggestion.active]?.scrollIntoView({ block: "nearest" });
  }

  function acceptSuggestion(index = suggestion.active) {
    const { items, token } = suggestion;
    const value = items[index];
    if (value === undefined) return;

    const text = ui.search.value;
    const inserted = /\s/.test(value) ? `"${value}"` : value;
    const rest = text.slice(token.end).replace(/^\s*/, " ");
    const caret = token.valueStart + inserted.length + 1;
    hideSuggestions();
    setSearch(text.slice(0, token.valueStart) + inserted + rest);
    ui.search.setSelectionRange(caret, caret);
  }

  function hideSuggestions() {
    ui.suggestions.hidden = true;
    suggestion = { items: [], active: 0, token: null };
  }

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

  // ------------------------------------------------------------------ methods

  const scheduleMethods = debounce(() => {
    if (!ui.methodsPop.hidden) renderMethods();
  }, 300);

  function renderMethods() {
    const query = ui.methodsSearch.value.trim().toLowerCase();
    const counts = new Map(state.methodCounts);
    // hidden methods stay listed so they can be shown again
    for (const method of state.methodFilter.set) if (!counts.has(method)) counts.set(method, 0);

    const methods = [...counts]
      .filter(([method]) => method.toLowerCase().includes(query))
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
    const busiest = Math.max(1, ...counts.values());

    ui.methodsList.innerHTML = methods.length
      ? methods
          .map(([method, count]) => {
            const shown = methodShown(method);
            return (
              `<li data-method="${escapeHtml(method)}" class="${shown ? "" : "off"}" title="Click to ${shown ? "hide" : "show"}">` +
              `<input type="checkbox" class="cb" tabindex="-1" ${shown ? "checked" : ""}>` +
              `<span class="name">${escapeHtml(method)}</span>` +
              '<button class="only" type="button" title="Only show this method">only</button>' +
              `<span class="meter"><span style="width: ${((count / busiest) * 100).toFixed(1)}%"></span></span>` +
              `<span class="count">${numberFormat.format(count)}</span></li>`
            );
          })
          .join("")
      : `<li class="none">${counts.size ? "No method matches" : "No traffic yet"}</li>`;

    ui.methodsSummary.textContent = methodsBadgeText();
  }

  function methodsBadgeText() {
    const { mode, set } = state.methodFilter;
    if (mode === "include") return `only ${set.size}`;
    return set.size ? `${set.size} hidden` : "";
  }

  function updateMethodsBadge() {
    const text = methodsBadgeText();
    ui.methodsBadge.textContent = text;
    ui.methodsBadge.hidden = !text;
  }

  function methodFilterChanged() {
    prefs.methods = { mode: state.methodFilter.mode, list: [...state.methodFilter.set] };
    savePrefs();
    updateMethodsBadge();
    if (!ui.methodsPop.hidden) renderMethods();
    refilter();
  }

  function setMethodFilter(mode, methods) {
    state.methodFilter = { mode, set: new Set(methods) };
    methodFilterChanged();
  }

  function toggleMethod(method) {
    const { set } = state.methodFilter;
    if (set.has(method)) set.delete(method);
    else set.add(method);
    methodFilterChanged();
  }

  function hideMethod(method) {
    const { mode, set } = state.methodFilter;
    if (mode === "include") set.delete(method);
    else set.add(method);
    methodFilterChanged();
  }

  function hideInspectedMethod() {
    if (!state.inspected) return;
    hideMethod(state.inspected.method);
    toast(`Hid ${state.inspected.method}`);
  }

  function toggleMethods() {
    if (!ui.methodsPop.hidden) {
      ui.methodsPop.hidden = true;
      return;
    }
    openPopover(ui.methodsPop, ui.methodsBtn);
    ui.methodsSearch.value = "";
    renderMethods();
    ui.methodsSearch.focus();
  }

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

  // ------------------------------------------------------------------ popovers

  function openPopover(popover, anchor) {
    closePopovers();
    popover.hidden = false;
    popover._anchor = anchor;
    const rect = anchor.getBoundingClientRect();
    // below the anchor, or above it if it sits at the bottom of the window
    const fitsBelow = rect.bottom + 6 + popover.offsetHeight <= window.innerHeight - 8;
    popover.style.top = `${fitsBelow ? rect.bottom + 6 : Math.max(8, rect.top - 6 - popover.offsetHeight)}px`;
    popover.style.left = `${clamp(rect.left, 8, window.innerWidth - popover.offsetWidth - 8)}px`;
  }

  function closePopovers() {
    let closed = false;
    for (const popover of [ui.methodsPop, ui.helpPop]) {
      if (!popover.hidden) {
        popover.hidden = true;
        closed = true;
      }
    }
    return closed;
  }

  function toggleHelp(anchor = ui.helpBtn) {
    if (ui.helpPop.hidden) openPopover(ui.helpPop, anchor);
    else ui.helpPop.hidden = true;
  }

  document.addEventListener("pointerdown", (event) => {
    for (const popover of [ui.methodsPop, ui.helpPop]) {
      if (!popover.hidden && !popover.contains(event.target) && !popover._anchor?.contains(event.target)) {
        popover.hidden = true;
      }
    }
  });

  // ------------------------------------------------------------------ toolbar and inspector controls

  function setFirstOnly(value) {
    prefs.firstOnly = value;
    savePrefs();
    ui.firstOnly.checked = value;
    refilter();
  }

  function setPaused(paused) {
    state.paused = paused;
    ui.pauseBtn.setAttribute("aria-pressed", String(paused));
    ui.pauseBtn.title = paused ? "Resume capturing (p)" : "Pause capturing (p)";
    ui.pausedNote.hidden = !paused;
    renderEmpty();
  }

  function setTab(tab) {
    prefs.tab = tab;
    savePrefs();
    for (const button of ui.tabs.querySelectorAll("[data-tab]")) {
      button.setAttribute("aria-selected", String(button.dataset.tab === tab));
    }
    ui.treeTools.hidden = tab !== "tree";
    renderInspectorBody();
  }

  function resetFilters() {
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

  const lightScheme = matchMedia("(prefers-color-scheme: light)");
  function applyTheme() {
    document.documentElement.dataset.theme =
      prefs.theme === "system" ? (lightScheme.matches ? "light" : "dark") : prefs.theme;
    ui.themeBtn.dataset.mode = prefs.theme;
    ui.themeBtn.title = `${THEME_NAMES[prefs.theme]}. Click to switch`;
  }
  lightScheme.addEventListener("change", applyTheme);

  function switchTheme() {
    const before = document.documentElement.dataset.theme;
    prefs.theme = THEMES[(THEMES.indexOf(prefs.theme) + 1) % THEMES.length];
    savePrefs();
    toast(THEME_NAMES[prefs.theme]);

    const theme = prefs.theme === "system" ? (lightScheme.matches ? "light" : "dark") : prefs.theme;
    const animate =
      document.startViewTransition && theme !== before && !matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (!animate) {
      applyTheme();
      return;
    }

    // the new colors spread out from the button in a circle
    const button = ui.themeBtn.getBoundingClientRect();
    const keyframes = revealKeyframes(button.left + button.width / 2, button.top + button.height / 2);
    document.startViewTransition(applyTheme).ready.then(() => {
      // eases the uncovered area, not the radius: quick at first, and it still moves at the end
      document.documentElement.animate(keyframes, {
        duration: 320,
        easing: "ease-out",
        pseudoElement: "::view-transition-new(root)",
      });
    });
  }

  // A circle that uncovers the same share of the page in every moment. Easing its radius instead
  // leaves the far corner for last, and the edge crawls over it while the rest is long done
  function revealKeyframes(x, y) {
    const columns = 64;
    const rows = 36;
    const distances = [];
    for (let column = 0; column < columns; column++) {
      for (let row = 0; row < rows; row++) {
        distances.push(Math.hypot(((column + 0.5) / columns) * innerWidth - x, ((row + 0.5) / rows) * innerHeight - y));
      }
    }
    distances.sort((a, b) => a - b);

    const farthest = Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y));
    const steps = 30;
    return Array.from({ length: steps + 1 }, (_, step) => {
      const radius =
        step === 0 ? 0 : step === steps ? farthest : distances[Math.round((step / steps) * (distances.length - 1))];
      return { clipPath: `circle(${radius.toFixed(1)}px at ${x}px ${y}px)` };
    });
  }

  function applyListWidth() {
    if (prefs.listWidth) ui.main.style.setProperty("--list-width", `${prefs.listWidth * 100}%`);
    else ui.main.style.removeProperty("--list-width");
  }

  ui.firstOnly.addEventListener("change", () => setFirstOnly(ui.firstOnly.checked));
  ui.pauseBtn.addEventListener("click", () => send({ type: "pause", value: !state.paused }));
  ui.follow.addEventListener("change", () => setFollow(ui.follow.checked));
  ui.clearBtn.addEventListener("click", () => send({ type: "clear" }));
  ui.helpBtn.addEventListener("click", () => toggleHelp(ui.helpBtn));
  ui.shortcutsBtn.addEventListener("click", () => toggleHelp(ui.shortcutsBtn));
  ui.themeBtn.addEventListener("click", switchTheme);
  ui.exportBtn.addEventListener("click", () => {
    if (state.records.length) toast(`Exporting ${plural(state.records.length, "request")}`);
  });
  ui.receiver.addEventListener("click", () => copy(state.receiver, "the receiver address"));
  ui.logoutBtn.addEventListener("click", () => {
    fetch("logout", { method: "POST" }).finally(() => location.assign("login"));
  });
  ui.newPill.addEventListener("click", () => setFollow(true));

  ui.logEmpty.addEventListener("click", (event) => {
    const action = event.target.closest("[data-action]")?.dataset.action;
    if (action === "resume") send({ type: "pause", value: false });
    else if (action === "reset-filters") resetFilters();
  });

  ui.logHead.addEventListener("click", (event) => {
    const button = event.target.closest("[data-sort]");
    if (button) setSort(button.dataset.sort);
  });

  ui.rows.addEventListener("click", (event) => {
    const element = event.target.closest(".row");
    if (!element) return;
    const position = Number(element.dataset.position);
    // inspecting an older row stops following, otherwise it would scroll away
    if (position !== edgePosition()) setFollow(false);
    select(state.visible[position]);
  });

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
    ui.inspectorBody.classList.toggle("no-types", !prefs.types);
  });
  ui.expandBtn.addEventListener("click", () => setTreeOpen(true));
  ui.collapseBtn.addEventListener("click", () => setTreeOpen(false));
  ui.copyJsonBtn.addEventListener("click", () => copyInspected("json"));
  ui.copyTextBtn.addEventListener("click", () => copyInspected("text"));

  ui.inspectorHead.addEventListener("click", (event) => {
    const action = event.target.closest("[data-action]")?.dataset.action;
    if (action === "hide-method") hideInspectedMethod();
    else if (action === "only-method" && state.inspected) setMethodFilter("include", [state.inspected.method]);
  });

  ui.inspectorBody.addEventListener("click", (event) => {
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

  // ------------------------------------------------------------------ keyboard

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
    if (event.ctrlKey || event.metaKey || event.altKey) return;

    const shortcut = shortcuts[event.key];
    if (!shortcut) return;
    event.preventDefault();
    shortcut();
  });

  // ------------------------------------------------------------------ connection

  let socket = null;
  let reconnectDelay = 500;

  function connect() {
    const url = new URL("ws", location.href);
    url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
    socket = new WebSocket(url);
    socket.addEventListener("open", () => {
      reconnectDelay = 500;
      setConnected(true);
    });
    socket.addEventListener("message", (event) => handleMessage(JSON.parse(event.data)));
    socket.addEventListener("close", () => {
      setConnected(false);
      // a session that ran out, or a new password, can't connect anymore
      fetch("api/session")
        .then((response) => {
          if (response.status === 401) location.assign("login");
        })
        .catch(() => {});
      setTimeout(connect, reconnectDelay);
      reconnectDelay = Math.min(reconnectDelay * 2, 5000);
    });
  }

  function send(command) {
    if (socket?.readyState !== WebSocket.OPEN) {
      toast("Not connected to Traffic Light");
      return;
    }
    socket.send(JSON.stringify(command));
  }

  function setConnected(connected) {
    state.connected = connected;
    ui.conn.classList.toggle("connected", connected);
    ui.conn.classList.toggle("disconnected", !connected);
    ui.conn.querySelector(".label").textContent = connected ? "Connected" : "Reconnecting…";
    renderEmpty();
  }

  function handleMessage(message) {
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
        break;
      }
      case "history":
        addRecords(message.records, true);
        break;
      case "records":
        addRecords(message.records);
        break;
      case "clear":
        resetRecords();
        clearInspector();
        toast("Log cleared");
        break;
      case "state":
        setPaused(message.paused);
        break;
    }
  }

  // ------------------------------------------------------------------ start

  ui.search.value = prefs.query;
  ui.searchClear.hidden = !prefs.query;
  ui.search.classList.toggle("invalid", Boolean(state.query.error));
  ui.firstOnly.checked = prefs.firstOnly;
  ui.typesToggle.checked = prefs.types;
  setTab(prefs.tab);
  updateSortUi();
  setFollow(state.follow);
  applyTheme();
  applyListWidth();
  updateMethodsBadge();
  renderList();
  connect();
})();

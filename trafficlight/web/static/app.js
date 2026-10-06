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
  const THEMES = ["system", "light", "dark"];
  const THEME_ICONS = {
    system:
      '<svg viewBox="0 0 16 16" aria-hidden="true"><path fill-rule="evenodd" d="M8 1a7 7 0 1 1 0 14A7 7 0 0 1 8 1Zm0 1.5v11a5.5 5.5 0 0 0 0-11Z"/></svg>',
    light:
      '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 4.5a3.5 3.5 0 1 1 0 7 3.5 3.5 0 0 1 0-7ZM7.25 0h1.5v2.5h-1.5zM7.25 13.5h1.5V16h-1.5zM0 7.25h2.5v1.5H0zM13.5 7.25H16v1.5h-2.5zM2.1 3.16l1.06-1.06 1.77 1.77-1.06 1.06zM11.07 12.13l1.06-1.06 1.77 1.77-1.06 1.06zM2.1 12.84l1.77-1.77 1.06 1.06-1.77 1.77zM11.07 3.87l1.77-1.77 1.06 1.06-1.77 1.77z"/></svg>',
    dark: '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6.2 1.2a6.8 6.8 0 1 0 8.6 8.6A5.6 5.6 0 0 1 6.2 1.2Z"/></svg>',
  };

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
    firstBtn: $("first-btn"),
    pauseBtn: $("pause-btn"),
    followBtn: $("follow-btn"),
    clearBtn: $("clear-btn"),
    themeBtn: $("theme-btn"),
    main: $("main"),
    viewport: $("viewport"),
    sizer: $("sizer"),
    rows: $("rows"),
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
    ...readPrefs(),
  };
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
    trimRecords();

    let matching = 0;
    for (const row of added) {
      if (row.dropped || !matches(row)) continue;
      state.visible.push(row);
      matching++;
    }
    if (!history && !state.follow && matching && state.visible.length * ROW_HEIGHT > ui.viewport.clientHeight) {
      state.unseen += matching;
    }

    if (state.restoreKey) {
      const row = added.find((candidate) => candidate.key === state.restoreKey);
      if (row) {
        state.restoreKey = null;
        state.selected = row;
        const position = state.visible.indexOf(row);
        if (!state.follow && position !== -1) {
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

    let droppedVisible = 0;
    while (droppedVisible < state.visible.length && state.visible[droppedVisible].dropped) droppedVisible++;
    if (droppedVisible) {
      state.visible.splice(0, droppedVisible);
      // keep the same rows in view
      if (!state.follow) ui.viewport.scrollTop -= droppedVisible * ROW_HEIGHT;
    }
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
    if (state.follow) viewport.scrollTop = viewport.scrollHeight;

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
        '<div class="icon" aria-hidden="true">🚦</div><div class="title">Waiting for traffic…</div>' +
        `<div>Set your MITM's POST destination to <code>${escapeHtml(state.receiver)}</code></div>`;
    }
  }

  function renderStats() {
    const total = state.rows.length;
    const shown = state.visible.length;
    const protos = shown === total ? plural(total, "proto") : `${numberFormat.format(shown)} of ${plural(total, "proto")}`;
    ui.stats.textContent = `${protos} · ${plural(state.records.length, "request")}`;
  }

  function updateNewPill() {
    const show = !state.follow && state.unseen > 0;
    ui.newPill.hidden = !show;
    if (show) ui.newPill.textContent = `↓ ${plural(state.unseen, "new proto")}`;
  }

  function setFollow(follow) {
    state.follow = follow;
    ui.followBtn.setAttribute("aria-pressed", String(follow));
    if (follow) {
      state.unseen = 0;
      // rendering scrolls to the bottom while following
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
    if (target < count - 1) setFollow(false);
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

    ui.methodsList.innerHTML = methods.length
      ? methods
          .map(([method, count]) => {
            const shown = methodShown(method);
            return (
              `<li data-method="${escapeHtml(method)}" class="${shown ? "" : "off"}" title="Click to ${shown ? "hide" : "show"}">` +
              `<input type="checkbox" tabindex="-1" ${shown ? "checked" : ""}>` +
              `<span class="name">${escapeHtml(method)}</span>` +
              '<button class="only" type="button" title="Only show this method">only</button>' +
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
    const rect = anchor.getBoundingClientRect();
    popover.style.top = `${rect.bottom + 6}px`;
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

  function toggleHelp() {
    if (ui.helpPop.hidden) openPopover(ui.helpPop, ui.helpBtn);
    else ui.helpPop.hidden = true;
  }

  document.addEventListener("pointerdown", (event) => {
    for (const [popover, anchor] of [
      [ui.methodsPop, ui.methodsBtn],
      [ui.helpPop, ui.helpBtn],
    ]) {
      if (!popover.hidden && !popover.contains(event.target) && !anchor.contains(event.target)) popover.hidden = true;
    }
  });

  // ------------------------------------------------------------------ toolbar and inspector controls

  function setFirstOnly(value) {
    prefs.firstOnly = value;
    savePrefs();
    ui.firstBtn.setAttribute("aria-pressed", String(value));
    refilter();
  }

  function setPaused(paused) {
    state.paused = paused;
    ui.pauseBtn.setAttribute("aria-pressed", String(paused));
    ui.pauseBtn.querySelector(".label").textContent = paused ? "Paused" : "Live";
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
    ui.firstBtn.setAttribute("aria-pressed", "false");
    state.methodFilter = { mode: "exclude", set: new Set() };
    methodFilterChanged();
    renderInspectorBody();
  }

  const lightScheme = matchMedia("(prefers-color-scheme: light)");
  function applyTheme() {
    document.documentElement.dataset.theme =
      prefs.theme === "system" ? (lightScheme.matches ? "light" : "dark") : prefs.theme;
    ui.themeBtn.innerHTML = THEME_ICONS[prefs.theme];
    ui.themeBtn.title = `Theme: ${prefs.theme}. Click to switch`;
  }
  lightScheme.addEventListener("change", applyTheme);

  function applyListWidth() {
    if (prefs.listWidth) ui.main.style.setProperty("--list-width", `${prefs.listWidth * 100}%`);
    else ui.main.style.removeProperty("--list-width");
  }

  ui.firstBtn.addEventListener("click", () => setFirstOnly(!prefs.firstOnly));
  ui.pauseBtn.addEventListener("click", () => send({ type: "pause", value: !state.paused }));
  ui.followBtn.addEventListener("click", () => setFollow(!state.follow));
  ui.clearBtn.addEventListener("click", () => send({ type: "clear" }));
  ui.helpBtn.addEventListener("click", toggleHelp);
  ui.themeBtn.addEventListener("click", () => {
    prefs.theme = THEMES[(THEMES.indexOf(prefs.theme) + 1) % THEMES.length];
    savePrefs();
    applyTheme();
  });
  ui.receiver.addEventListener("click", () => copy(state.receiver, "the receiver address"));
  ui.newPill.addEventListener("click", () => setFollow(true));

  ui.logEmpty.addEventListener("click", (event) => {
    const action = event.target.closest("[data-action]")?.dataset.action;
    if (action === "resume") send({ type: "pause", value: false });
    else if (action === "reset-filters") resetFilters();
  });

  ui.rows.addEventListener("click", (event) => {
    const element = event.target.closest(".row");
    if (!element) return;
    const position = Number(element.dataset.position);
    // inspecting an older row stops following, otherwise it would scroll away
    if (position < state.visible.length - 1) setFollow(false);
    select(state.visible[position]);
  });

  ui.viewport.addEventListener(
    "scroll",
    () => {
      const viewport = ui.viewport;
      // following means sticking to the bottom: scrolling away stops it, scrolling back starts it again
      const atBottom = viewport.scrollTop + viewport.clientHeight >= viewport.scrollHeight - 4;
      if (atBottom !== state.follow) setFollow(atBottom);
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
    Home: () => moveTo(0),
    End: () => {
      moveTo(Infinity);
      setFollow(true);
    },
    p: () => send({ type: "pause", value: !state.paused }),
    f: () => setFollow(!state.follow),
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
    if (event.isComposing || event.target.closest?.("input, textarea, select")) return;
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
    ui.conn.className = `conn ${connected ? "connected" : "disconnected"}`;
    ui.conn.querySelector(".label").textContent = connected ? "Connected" : "Reconnecting…";
    renderEmpty();
  }

  function handleMessage(message) {
    switch (message.type) {
      case "hello": {
        // the server sends its whole log after this. After a restart, nothing from before is valid anymore
        const restarted = state.session !== null && state.session !== message.session;
        state.restoreKey = restarted ? null : state.selected?.key ?? null;
        if (restarted) {
          detailCache.clear();
          clearInspector();
        }
        state.session = message.session;
        state.maxRecords = message.max_records;
        state.receiver = message.receiver;
        ui.receiver.textContent = `Receiver ${message.receiver}`;
        ui.receiver.hidden = false;
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
  ui.firstBtn.setAttribute("aria-pressed", String(prefs.firstOnly));
  ui.typesToggle.checked = prefs.types;
  setTab(prefs.tab);
  applyTheme();
  applyListWidth();
  updateMethodsBadge();
  renderList();
  connect();
})();

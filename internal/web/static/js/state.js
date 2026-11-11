// @ts-check
import { prefs } from "./prefs.js";
/** @typedef {import("./types.js").LogRecord} LogRecord */
/** @typedef {import("./types.js").Row} Row */
/** @typedef {import("./types.js").Tab} Tab */



export const state = {
  session: null,
  connected: false,
  paused: false,
  receiver: "",
  maxRecords: Infinity,
  /** @type {LogRecord[]} */
  records: [],
  /** @type {Row[]} one per proto, oldest first */
  rows: [],
  /** @type {Row[]} rows that pass the filters */
  visible: [],
  /** @type {Row|null} row highlighted in the log */
  selected: null,
  /** @type {Row|null} row shown in the inspector */
  inspected: null,
  /** @type {string|null} selection to restore after reconnecting */
  restoreKey: null,
  restoreFollow: true, // whether the log followed before reconnecting
  follow: true,
  unseen: 0,
  /** @type {{terms: object[], error: string|null}} set from prefs once everything is loaded */
  query: { terms: [], error: null },
  methodFilter: {
    mode: prefs.methods?.mode === "include" ? "include" : "exclude",
    set: new Set(Array.isArray(prefs.methods?.list) ? prefs.methods.list : []),
  },
  /** @type {Set<number>} ids of the records the server is keeping for us */
  saved: new Set(),
  methodCounts: new Map(),
  messageNames: new Set(),
  statuses: new Set(),
  /** mirrors the active tab, so everything outside the inspector can stay as it was */
  detail: null,
  /** @type {{tabs: Tab[], activeId: number|null, splitId: number|null, focusId: number|null, diff: boolean}} */
  panes: { tabs: [], activeId: null, splitId: null, focusId: null, diff: false },
};

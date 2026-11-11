// @ts-check
import { ROW_HEIGHT } from "./constants.js";
import { center, insertVisible, keepInView, scheduleRender, setFollow, updateNewPill, updateSizer } from "./list.js";
import { scheduleMethods } from "./methods.js";
import { matches } from "./query.js";
import { makeRow } from "./rows.js";
import { followEdge } from "./sort.js";
import { state } from "./state.js";
import { ui } from "./ui.js";

// history is what the server already had when connecting, none of it is new
export function addRecords(records, history = false, dropped = null) {
  const added = [];
  for (const record of records) {
    // the gap to the request before it, in arrival order. Computed once, so sorting can't change it
    const previous = state.records[state.records.length - 1];
    record.delta = previous ? record.time - previous.time : null;
    record.rows = record.protos.map((proto, index) => makeRow(record, proto, index));
    state.records.push(record);
    for (const row of record.rows) {
      state.rows.push(row);
      added.push(row);
    }
  }
  let matching = [];
  keepInView(() => {
    // the server tells us what it evicted, so both sides keep exactly the same log
    if (dropped) dropRecords(dropped);
    else trimRecords();
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

export function dropRecords(ids) {
  forgetRecords(new Set(ids));
}

/**
 * The server keeps saved records past the cap, so the ones it drops are not always the oldest.
 * Guessing wrong would leave the browser showing records the server no longer serves
 */
function forgetRecords(gone) {
  if (!gone.size) return;

  let prefix = 0;
  while (prefix < state.records.length && gone.has(state.records[prefix].id)) prefix++;
  // with nothing saved the dropped records are the oldest ones, which stays a cheap splice
  const asPrefix = prefix === gone.size;

  let droppedRows = 0;
  for (const record of asPrefix ? state.records.slice(0, prefix) : state.records) {
    if (!gone.has(record.id)) continue;
    for (const row of record.rows) {
      row.dropped = true;
      droppedRows++;
      const count = state.methodCounts.get(row.method) - 1;
      if (count > 0) state.methodCounts.set(row.method, count);
      else state.methodCounts.delete(row.method);
    }
  }

  if (asPrefix) {
    state.records.splice(0, prefix);
    state.rows.splice(0, droppedRows);
  } else {
    state.records = state.records.filter((record) => !gone.has(record.id));
    state.rows = state.rows.filter((row) => !row.dropped);
  }
  // depending on the sorting, dropped rows can be anywhere in the log
  state.visible = state.visible.filter((row) => !row.dropped);
}

// fallback for a server that doesn't say what it dropped: work it out the same way it does
export function trimRecords() {
  const excess = state.records.length - state.maxRecords;
  if (excess <= 0) return;

  const gone = new Set();
  for (const record of state.records) {
    if (gone.size >= excess) break;
    if (!state.saved.has(record.id)) gone.add(record.id);
  }
  forgetRecords(gone);
}

export function resetRecords() {
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

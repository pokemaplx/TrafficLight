// @ts-check
import { renderInspectorHead } from "./inspector.js";
import { refilter, scheduleRender } from "./list.js";
import { send } from "./socket.js";
import { state } from "./state.js";
import { ui } from "./ui.js";
import { numberFormat, plural } from "./util.js";
/** @typedef {import("./types.js").Row} Row */

/** Asks the server to keep this request, or let it go. The server decides, this only follows */
export function toggleSaved(row) {
  if (!row) return;
  send({ type: "save", id: row.record.id, value: !state.saved.has(row.record.id) });
}

export function applySaved(id, saved) {
  if (saved) state.saved.add(id);
  else state.saved.delete(id);

  for (const row of state.rows) if (row.record.id === id) row.html = null;
  // the inspector shows the same thing as a button, so it has to follow
  if (state.inspected && state.inspected.record.id === id) renderInspectorHead(state.inspected);
  updateSavedChip();
  // only an is:saved filter cares which rows are saved, otherwise the icon is all that changed
  if (savedFilterActive()) refilter();
  else scheduleRender();
}

export function applySavedSet(ids) {
  state.saved = new Set(ids);
  for (const row of state.rows) row.html = null;
  updateSavedChip();
  if (savedFilterActive()) refilter();
  else scheduleRender();
}

export const savedFilterActive = () => state.query.terms.some((term) => term.key === "is" && term.value === "saved");

export function updateSavedChip() {
  const count = state.saved.size;
  ui.savedChip.hidden = count === 0;
  ui.savedChip.classList.toggle("on", savedFilterActive());
  ui.savedCount.textContent = numberFormat.format(count);
  ui.savedChip.title = savedFilterActive()
    ? "Showing only saved requests. Click to show everything again"
    : `${plural(count, "saved request")}. Click to show only those`;
}


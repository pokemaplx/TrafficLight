// @ts-check
import { refilter } from "./list.js";
import { openPopover } from "./popover.js";
import { prefs, savePrefs } from "./prefs.js";
import { methodShown } from "./query.js";
import { state } from "./state.js";
import { toast, ui } from "./ui.js";
import { debounce, escapeHtml, numberFormat } from "./util.js";

export const scheduleMethods = debounce(() => {
  if (!ui.methodsPop.hidden) renderMethods();
}, 300);

export function renderMethods() {
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

export function methodsBadgeText() {
  const { mode, set } = state.methodFilter;
  if (mode === "include") return `only ${set.size}`;
  return set.size ? `${set.size} hidden` : "";
}

export function updateMethodsBadge() {
  const text = methodsBadgeText();
  ui.methodsBadge.textContent = text;
  ui.methodsBadge.hidden = !text;
}

export function methodFilterChanged() {
  prefs.methods = { mode: state.methodFilter.mode, list: [...state.methodFilter.set] };
  savePrefs();
  updateMethodsBadge();
  if (!ui.methodsPop.hidden) renderMethods();
  refilter();
}

export function setMethodFilter(mode, methods) {
  state.methodFilter = { mode, set: new Set(methods) };
  methodFilterChanged();
}

export function toggleMethod(method) {
  const { set } = state.methodFilter;
  if (set.has(method)) set.delete(method);
  else set.add(method);
  methodFilterChanged();
}

export function hideMethod(method) {
  const { mode, set } = state.methodFilter;
  if (mode === "include") set.delete(method);
  else set.add(method);
  methodFilterChanged();
}

export function hideInspectedMethod() {
  if (!state.inspected) return;
  hideMethod(state.inspected.method);
  toast(`Hid ${state.inspected.method}`);
}

export function toggleMethods() {
  if (!ui.methodsPop.hidden) {
    ui.methodsPop.hidden = true;
    return;
  }
  openPopover(ui.methodsPop, ui.methodsBtn);
  ui.methodsSearch.value = "";
  renderMethods();
  ui.methodsSearch.focus();
}

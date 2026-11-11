// @ts-check
import { highlight } from "./highlight.js";
import { renderInspectorBody } from "./inspector.js";
import { refilter } from "./list.js";
import { prefs, savePrefs } from "./prefs.js";
import { savedFilterActive, updateSavedChip } from "./saved.js";
import { IS_VALUES, parseQuery, tokenize } from "./query.js";
import { state } from "./state.js";
import { ui } from "./ui.js";
import { debounce, escapeRegExp, numberFormat } from "./util.js";
/** @typedef {import("./types.js").Token} Token */

export const applySearch = debounce(() => {
  prefs.query = ui.search.value;
  savePrefs();
  state.query = parseQuery(prefs.query);
  ui.search.classList.toggle("invalid", Boolean(state.query.error));
  ui.search.title = state.query.error ? `Invalid regular expression: ${state.query.error}` : "";
  refilter();
  updateSavedChip();
  renderInspectorBody();
}, 150);

const SAVED_TERM = /\s*-?\bis:saved\b/i;

/** The saved chip is a shortcut for the filter term, so there is one way to filter, not two */
export function toggleSavedFilter() {
  const text = ui.search.value;
  setSearch(savedFilterActive() ? text.replace(SAVED_TERM, "").trim() : `${text} is:saved`.trim());
}

export function setSearch(value) {
  ui.search.value = value;
  ui.searchClear.hidden = !value;
  applySearch();
  applySearch.flush();
}

export function focusSearch() {
  ui.search.focus();
  ui.search.select();
}

/** @type {{items: string[], active: number, token: Token|null}} */
export let suggestion = { items: [], active: 0, token: null };

// completes values after method:, msg: and status:
export function updateSuggestions() {
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
    token.key === "method"
      ? [...counts.keys()]
      : token.key === "msg"
        ? [...state.messageNames]
        : token.key === "is"
          ? Object.keys(IS_VALUES)
          : [...state.statuses];
  const rank = (item) => (item.toLowerCase().startsWith(query) ? 0 : 1);
  const items = source
    .filter((item) => item.toLowerCase().includes(query) && item.toLowerCase() !== query)
    .sort((a, b) => rank(a) - rank(b) || (counts.get(b) || 0) - (counts.get(a) || 0) || a.localeCompare(b))
    .slice(0, 12);
  if (!items.length) return hideSuggestions();

  suggestion = { items, active: 0, token };
  renderSuggestions();
}

export function renderSuggestions() {
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

export function moveSuggestion(delta) {
  const count = suggestion.items.length;
  suggestion.active = (suggestion.active + delta + count) % count;
  renderSuggestions();
  ui.suggestions.children[suggestion.active]?.scrollIntoView({ block: "nearest" });
}

export function acceptSuggestion(index = suggestion.active) {
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

export function hideSuggestions() {
  ui.suggestions.hidden = true;
  suggestion = { items: [], active: 0, token: null };
}

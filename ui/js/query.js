// @ts-check
import { QUERY_KEYS } from "./constants.js";
import { prefs } from "./prefs.js";
import { state } from "./state.js";
import { escapeRegExp } from "./util.js";
/** @typedef {import("./types.js").Token} Token */
/** @typedef {import("./types.js").Row} Row */

// splits a query into tokens: [-][key:](word | "phrase" | /regex/)
export function tokenize(input) {
  /** @type {Token[]} */
  const tokens = [];
  let i = 0;
  while (i < input.length) {
    if (/\s/.test(input[i])) {
      i++;
      continue;
    }

    /** @type {Token} */
    const token = { start: i, negate: false, key: null, value: "", regex: null, valueStart: i, end: i };
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

export function parseQuery(input) {
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

/**
 * Values for the is: key. Anything else matches nothing, rather than matching everything
 * @type {Record<string, (row: Row) => boolean>}
 */
export const IS_VALUES = {
  saved: (row) => state.saved.has(row.record.id),
  proxy: (row) => row.via !== null,
  error: (row) => row.badge?.tone === "bad",
  ok: (row) => row.badge?.tone === "ok",
  empty: (row) => row.proto.response.state === "empty",
  unknown: (row) => row.proto.request.state === "unknown" || row.proto.response.state === "unknown",
};

export function matches(row) {
  if (prefs.firstOnly && row.index > 0) return false;
  if (!methodShown(row.method)) return false;

  for (const term of state.query.terms) {
    // is: asks a yes or no question about the row, it has no text to search through
    if (term.key === "is") {
      const found = (IS_VALUES[term.value] ?? (() => false))(row);
      if (found === term.negate) return false;
      continue;
    }
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

export function methodShown(method) {
  const { mode, set } = state.methodFilter;
  return set.has(method) === (mode === "include");
}

// terms worth highlighting in the inspector
export function highlightTerms() {
  return state.query.terms.filter((term) => !term.negate && !term.key);
}

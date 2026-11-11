// @ts-check
import { escapeHtml } from "./util.js";

// Every mark gets an id, and the pieces of one match share theirs, so a match split across
// syntax spans still counts once and is stepped through once. The counter just keeps running
let markId = 0;

/**
 * Where the terms match, merged into ranges that do not overlap, in order.
 * Finding them over the whole text first is what lets a match cross a syntax token
 */
export function markRanges(text, terms) {
  const found = [];
  for (const term of terms) {
    const regex = term.global;
    regex.lastIndex = 0;
    let match;
    while ((match = regex.exec(text)) !== null) {
      if (match[0] === "") {
        regex.lastIndex++;
        continue;
      }
      found.push({ start: match.index, end: match.index + match[0].length, cls: term.cls ?? "" });
    }
  }
  if (!found.length) return found;
  found.sort((a, b) => a.start - b.start || b.end - a.end);

  const merged = [];
  for (const range of found) {
    const last = merged[merged.length - 1];
    if (last && range.start < last.end) {
      // overlapping matches become one mark, and a find match keeps its colour
      last.end = Math.max(last.end, range.end);
      if (range.cls === "find") last.cls = "find";
    } else {
      merged.push({ ...range, id: markId++ });
    }
  }
  return merged;
}

export function highlight(text, terms) {
  text = String(text);
  if (!terms || !terms.length) return escapeHtml(text);
  const ranges = markRanges(text, terms);
  if (!ranges.length) return escapeHtml(text);
  return paint(text, ranges, 0, text.length, { i: 0 });
}

/**
 * The slice [from, to) of source, with any ranges that reach into it wrapped in a mark.
 * The cursor only moves forward, because callers walk the source in order
 */
function paint(source, ranges, from, to, cursor) {
  if (to <= from) return "";
  while (cursor.i < ranges.length && ranges[cursor.i].end <= from) cursor.i++;

  let html = "";
  let position = from;
  for (let i = cursor.i; i < ranges.length; i++) {
    const range = ranges[i];
    if (range.start >= to) break;
    const start = Math.max(range.start, position);
    const end = Math.min(range.end, to);
    if (end <= start) continue;
    html += escapeHtml(source.slice(position, start));
    html += `<mark${range.cls ? ` class="${range.cls}"` : ""} data-m="${range.id}">${escapeHtml(source.slice(start, end))}</mark>`;
    position = end;
  }
  return html + escapeHtml(source.slice(position, to));
}

/** Syntax colouring with the search marks laid over it, both without losing the other */
export function highlightCode(source, regex, kinds, terms) {
  const ranges = terms && terms.length ? markRanges(source, terms) : [];
  const cursor = { i: 0 };

  let html = "";
  let position = 0;
  for (const match of source.matchAll(regex)) {
    const kind = kinds[match.slice(1).findIndex((group) => group !== undefined)];
    html += paint(source, ranges, position, match.index, cursor);
    const end = match.index + match[0].length;
    html += `<span class="${kind}">${paint(source, ranges, match.index, end, cursor)}</span>`;
    position = end;
  }
  return html + paint(source, ranges, position, source.length, cursor);
}

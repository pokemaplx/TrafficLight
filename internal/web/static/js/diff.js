// @ts-check
import { escapeHtml } from "./util.js";

// an LCS table is one number per pair of lines, so very different protos fall back to a plain
// "all of this went, all of that arrived" instead of eating memory
const MAX_CELLS = 2_000_000;
// runs of identical lines longer than this are folded away unless you ask to see them
const CONTEXT = 2;

/**
 * Compares two protos line by line, on their JSON, which is the view that already exists and is
 * canonical. A structural walk would have to pair repeated entries by position, and then one
 * inserted element reads as everything after it having changed
 */
export function diffLines(a, b) {
  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;
  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--;
    endB--;
  }

  const head = a.slice(0, start).map((text) => ({ type: "same", text }));
  const tail = a.slice(endA).map((text) => ({ type: "same", text }));
  const midA = a.slice(start, endA);
  const midB = b.slice(start, endB);
  if (!midA.length && !midB.length) return [...head, ...tail];

  const middle =
    midA.length * midB.length > MAX_CELLS
      ? [...midA.map((text) => ({ type: "del", text })), ...midB.map((text) => ({ type: "add", text }))]
      : lcs(midA, midB);
  return [...head, ...middle, ...tail];
}

function lcs(a, b) {
  const n = a.length;
  const m = b.length;
  const width = m + 1;
  const table = new Uint32Array((n + 1) * width);
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      table[i * width + j] =
        a[i] === b[j]
          ? table[(i + 1) * width + j + 1] + 1
          : Math.max(table[(i + 1) * width + j], table[i * width + j + 1]);
    }
  }

  const out = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ type: "same", text: a[i] });
      i++;
      j++;
    } else if (table[(i + 1) * width + j] >= table[i * width + j + 1]) {
      out.push({ type: "del", text: a[i] });
      i++;
    } else {
      out.push({ type: "add", text: b[j] });
      j++;
    }
  }
  while (i < n) out.push({ type: "del", text: a[i++] });
  while (j < m) out.push({ type: "add", text: b[j++] });
  return out;
}

const messageLines = (message) => JSON.stringify(message?.json ?? null, null, 2).split("\n");

/** @returns {{element: HTMLElement, changes: number}} */
export function renderDiff(left, right, showUnchanged) {
  const container = document.createElement("div");
  container.className = "diff";

  const pairs = [
    ["Request", left.request, right.request],
    ["Response", left.response, right.response],
  ];
  if (left.proxy || right.proxy) {
    pairs.push(
      ["Proxy request", left.proxy?.request, right.proxy?.request],
      ["Proxy response", left.proxy?.response, right.proxy?.response],
    );
  }

  let changes = 0;
  for (const [name, a, b] of pairs) {
    const lines = diffLines(messageLines(a), messageLines(b));
    changes += lines.filter((line) => line.type !== "same").length;
    container.insertAdjacentHTML("beforeend", diffSection(name, a, b, lines, showUnchanged));
  }
  return { element: container, changes };
}

function diffSection(name, a, b, lines, showUnchanged) {
  const changed = lines.filter((line) => line.type !== "same").length;
  const summary = changed
    ? `<span class="diff-count"><span class="add">+${lines.filter((l) => l.type === "add").length}</span>` +
      `<span class="del">−${lines.filter((l) => l.type === "del").length}</span></span>`
    : '<span class="diff-same">identical</span>';

  return (
    '<section class="section open"><div class="section-head">' +
    `<span class="section-title">${name}</span>` +
    `<span class="section-name">${escapeHtml(a?.name || b?.name || "Unknown message")}</span>` +
    summary +
    '</div><div class="section-body">' +
    `<pre class="code diff-body">${diffBody(lines, showUnchanged)}</pre>` +
    "</div></section>"
  );
}

function diffBody(lines, showUnchanged) {
  const parts = [];
  let run = [];

  const flushRun = () => {
    if (!run.length) return;
    if (showUnchanged || run.length <= CONTEXT * 2 + 1) {
      for (const line of run) parts.push(lineHtml("same", line.text));
    } else {
      for (const line of run.slice(0, CONTEXT)) parts.push(lineHtml("same", line.text));
      parts.push(`<div class="diff-line gap">⋯ ${run.length - CONTEXT * 2} unchanged lines</div>`);
      for (const line of run.slice(-CONTEXT)) parts.push(lineHtml("same", line.text));
    }
    run = [];
  };

  for (const line of lines) {
    if (line.type === "same") run.push(line);
    else {
      flushRun();
      parts.push(lineHtml(line.type, line.text));
    }
  }
  flushRun();
  return parts.join("");
}

function lineHtml(type, text) {
  const sign = type === "add" ? "+" : type === "del" ? "−" : " ";
  return `<div class="diff-line ${type}"><span class="diff-sign">${sign}</span>${escapeHtml(text)}</div>`;
}

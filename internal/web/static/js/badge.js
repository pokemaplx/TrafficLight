// @ts-check
import { escapeHtml } from "./util.js";

export function badgeHtml(badge) {
  return `<span class="status-badge ${badge.tone}" title="${escapeHtml(badge.text)}">${escapeHtml(badge.text)}</span>`;
}

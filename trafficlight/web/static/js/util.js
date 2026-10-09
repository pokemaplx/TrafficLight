// @ts-check
import { TIME_ZONE } from "./constants.js";

export const numberFormat = new Intl.NumberFormat();
export const plural = (count, word) => `${numberFormat.format(count)} ${word}${count === 1 ? "" : "s"}`;
const HTML_ESCAPES = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };
export const escapeHtml = (value) => String(value).replace(/[&<>"']/g, (char) => HTML_ESCAPES[char]);
export const escapeRegExp = (value) => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
export const clamp = (value, min, max) => Math.min(Math.max(value, min), max);

export function formatBytes(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10240 ? 1 : 0)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

const pad = (value, length = 2) => String(value).padStart(length, "0");

export function formatTime(ms) {
  const date = new Date(ms);
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`;
}

export function formatDate(ms) {
  const date = new Date(ms);
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

// records are timestamped by Traffic Light and shown in the browser's zone, which can be another one
export function formatFullTime(ms) {
  return `${formatDate(ms)} ${formatTime(ms)} ${TIME_ZONE}`;
}

// how long after the request before it this one arrived
export function formatDelta(ms) {
  if (ms < 1000) return `+${ms} ms`;
  const seconds = ms / 1000;
  if (seconds < 10) return `+${seconds.toFixed(2)} s`;
  // the unit follows the rounded value, so 59.999 s reads as a minute instead of "+60.0 s"
  if (Math.round(seconds) < 60) return `+${seconds.toFixed(1)} s`;
  const whole = Math.round(seconds);
  return `+${Math.floor(whole / 60)} m ${pad(whole % 60)} s`;
}

export function debounce(fn, wait) {
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

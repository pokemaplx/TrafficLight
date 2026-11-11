// @ts-check
/** Values shared across the app. Keep ROW_HEIGHT in sync with --row-h in style.css. */

export const ROW_HEIGHT = 46; // keep in sync with --row-h in style.css
export const OVERSCAN = 10;
export const PREVIEW_LENGTH = 300;
export const DETAIL_CACHE_SIZE = 30;
export const SMALL_MESSAGE = 250; // messages with up to this many values are expanded completely
export const CODE_LIMIT = 1_000_000; // characters the JSON and Text views render before truncating
export const STORAGE_KEY = "trafficlight";
export const QUERY_KEYS = {
  method: "method",
  m: "method",
  msg: "msg",
  message: "msg",
  status: "status",
  s: "status",
  is: "is",
};
export const TABS = ["tree", "json", "text"];
// the columns the log can be sorted by, and whether a column sorts descending when it's picked
export const SORT_FIRST_DESC = { time: false, method: false, status: false, size: true };
// each open tab holds a whole decoded proto, so this is a memory budget as much as a UI one
export const MAX_TABS = 8;
export const THEMES = ["system", "light", "dark"];
// what the first column of the log shows: the clock, or the gap to the request before
export const TIME_MODES = ["clock", "delta"];
export const TIME_ZONE = Intl.DateTimeFormat().resolvedOptions().timeZone;
export const THEME_NAMES = { system: "System theme", light: "Light theme", dark: "Dark theme" };

// [regex, css class per capture group]
export const JSON_SYNTAX = [
  /("(?:[^"\\]|\\.)*")(?=\s*:)|("(?:[^"\\]|\\.)*")|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false|null)\b|([{}[\],])/g,
  ["k", "s", "n", "b", "p"],
];
export const TEXT_SYNTAX = [
  /("(?:[^"\\]|\\.)*")|([A-Za-z_]\w*)(?=:| \{)|(-?\d[\w.+-]*)|\b(true|false)\b|([A-Za-z_]\w*)|([{}])/g,
  ["s", "k", "n", "b", "e", "p"],
];

// @ts-check
import { STORAGE_KEY } from "./constants.js";

export const prefs = {
  theme: "system",
  query: "",
  firstOnly: false,
  tab: "tree",
  types: true,
  listWidth: null,
  split: false,
  splitRatio: null,
  timeMode: "clock",
  methods: { mode: "exclude", list: [] },
  sort: { key: "time", desc: false },
  ...readPrefs(),
};
function readPrefs() {
  try {
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY));
    return stored && typeof stored === "object" ? stored : {};
  } catch {
    return {};
  }
}

export function savePrefs() {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
  } catch {
    // storage can be unavailable, preferences just won't stick
  }
}

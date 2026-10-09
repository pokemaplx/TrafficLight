// @ts-check
import { prefs } from "./prefs.js";
import { state } from "./state.js";

const compareText = (a, b) => (a < b ? -1 : a > b ? 1 : 0);
export const SORT_COMPARE = {
  time: (a, b) => a.seq - b.seq,
  method: (a, b) => compareText(a.method, b.method),
  status: (a, b) => compareText(a.badge?.text ?? "", b.badge?.text ?? ""),
  size: (a, b) => a.proto.response.size - b.proto.response.size,
};

export function compareRows(a, b) {
  const { key, desc } = prefs.sort;
  // rows without a status go last either way
  if (key === "status" && !a.badge !== !b.badge) return a.badge ? -1 : 1;
  const result = SORT_COMPARE[key](a, b);
  return (desc ? -result : result) || a.seq - b.seq;
}

export const sortedByArrival = () => prefs.sort.key === "time" && !prefs.sort.desc;

// where new rows show up, so where following sticks to. Other columns put them anywhere
export function followEdge() {
  if (prefs.sort.key !== "time") return null;
  return prefs.sort.desc ? "top" : "bottom";
}

export function edgePosition() {
  const edge = followEdge();
  return edge === "top" ? 0 : edge === "bottom" ? state.visible.length - 1 : -1;
}

export function insertionPoint(row) {
  let low = 0;
  let high = state.visible.length;
  while (low < high) {
    const middle = (low + high) >> 1;
    if (compareRows(state.visible[middle], row) <= 0) low = middle + 1;
    else high = middle;
  }
  return low;
}

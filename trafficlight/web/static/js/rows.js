// @ts-check
import { PREVIEW_LENGTH } from "./constants.js";
import { state } from "./state.js";
import { formatTime } from "./util.js";

let rowSequence = 0; // arrival order

export function makeRow(record, proto, index) {
  const inner = proto.proxy || proto;
  const method = inner.method || `UNKNOWN_${inner.value}`;
  const messages = proto.proxy
    ? [proto.request, proto.response, inner.request, inner.response]
    : [proto.request, proto.response];
  const names = messages.map((message) => message.name).filter(Boolean);
  const badge = rowBadge(proto, inner);
  // what the row shows has to be searchable too, i.e. UNKNOWN_123 or "decode error"
  const statusText = [proto.status, badge?.text].filter(Boolean).join(" ").toLowerCase();

  names.forEach((name) => state.messageNames.add(name));
  if (proto.status) state.statuses.add(proto.status);
  state.methodCounts.set(method, (state.methodCounts.get(method) || 0) + 1);

  const row = {
    key: `${record.id}:${index}`,
    seq: rowSequence++,
    record,
    index,
    proto,
    method,
    value: inner.value,
    via: proto.proxy ? `${proto.method || "UNKNOWN"} ${proto.value}` : null,
    badge,
    time: formatTime(record.time),
    preview: [previewOf(inner.request), previewOf(inner.response)],
    methodText: [method, proto.method, proto.value, proto.proxy?.value]
      .filter((part) => part != null)
      .join(" ")
      .toLowerCase(),
    messageText: names.join(" ").toLowerCase(),
    statusText,
    searchText: [method, proto.method, ...names, statusText, ...messages.map((message) => message.text)]
      .filter(Boolean)
      .join("\n")
      .toLowerCase(),
    html: null,
  };

  // the inspector loads complete messages on its own, only the search text is needed from here on
  messages.forEach((message) => {
    message.text = undefined;
  });
  return row;
}

export function previewOf(message) {
  return message.text ? message.text.slice(0, PREVIEW_LENGTH) : "{}";
}

export function rowBadge(proto, inner) {
  const states = [inner.request.state, inner.response.state];
  if (states.includes("error")) return { text: "decode error", tone: "bad" };
  if (states.includes("unknown")) return { text: "unknown", tone: "warn" };
  if (proto.status) return { text: proto.status, tone: statusTone(proto.status) };
  if (inner.response.state === "empty") return { text: "empty", tone: "muted" };
  return null;
}

export function statusTone(status) {
  if (/SUCCESS|COMPLETED|(^|_)OK$/.test(status)) return "ok";
  if (/ERROR|FAIL|INVALID|DENIED|UNAUTHORIZED|NOT_FOUND|TIMEOUT|RATE_LIMITED|BANNED/.test(status)) return "bad";
  return "";
}

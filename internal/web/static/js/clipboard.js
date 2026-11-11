// @ts-check
import { state } from "./state.js";
import { toast } from "./ui.js";

export async function copy(text, what) {
  try {
    if (navigator.clipboard && window.isSecureContext) await navigator.clipboard.writeText(text);
    else legacyCopy(text);
    toast(`Copied ${what}`);
  } catch {
    toast("Couldn't copy to the clipboard");
  }
}

// the clipboard api only exists on secure origins, which a LAN address isn't
export function legacyCopy(text) {
  const area = document.createElement("textarea");
  area.value = text;
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.append(area);
  area.select();
  const copied = document.execCommand("copy");
  area.remove();
  if (!copied) throw new Error("copy failed");
}

export function protoJson(detail) {
  const message = (m) => ({ name: m.name, data: m.json });
  return {
    method: detail.method,
    value: detail.value,
    request: message(detail.request),
    response: message(detail.response),
    ...(detail.proxy ? { proxy: protoJson(detail.proxy) } : {}),
  };
}

// same layout as copying in the terminal UI
export function protoText(detail) {
  const message = (m) => {
    const name = m.name || "Unknown message";
    if (!m.tree) return `${name} ${m.text}`;
    const body = m.text.trimEnd();
    return body ? `${name} {\n${body.replace(/^/gm, "  ")}\n}` : `${name} {}`;
  };
  let text = `${detail.method || "Unknown method"} | ${detail.value}\n\n${message(detail.request)}\n\n${message(detail.response)}`;
  if (detail.proxy) text += `\n\nProxy: ${protoText(detail.proxy)}`;
  return text;
}

export function copyInspected(format) {
  if (!state.detail) return;
  if (format === "json") copy(JSON.stringify(protoJson(state.detail), null, 2), "proto as JSON");
  else copy(protoText(state.detail), "proto as text");
}

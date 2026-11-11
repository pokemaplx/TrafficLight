// @ts-check
import { SMALL_MESSAGE } from "./constants.js";
import { highlight } from "./highlight.js";
import { escapeHtml, plural } from "./util.js";

// nodes come from the server (see serialize.py) or from jsonToNode for messages without a definition
export function renderTree(root, context) {
  const tree = document.createElement("div");
  tree.className = "tree";
  appendFields(tree, root, 1, { ...context, openDepth: isSmall(root) ? Infinity : 1 });
  return tree;
}

export function isSmall(message) {
  let budget = SMALL_MESSAGE;
  const visit = (node) => {
    for (const [, , repeated, value] of node.f) {
      for (const item of repeated ? value : [value]) {
        if (--budget < 0) return false;
        if (item.k === "msg" && !visit(item)) return false;
      }
    }
    return true;
  };
  return visit(message);
}

export function appendFields(container, message, depth, context) {
  // with "matches only" on, a field is only worth drawing when it or something under it matches
  const fields = context.matchesOnly && context.matcher
    ? message.f.filter(([name, , repeated, value]) =>
        context.matcher.text(name) || (repeated ? value.some(context.matcher.value) : context.matcher.value(value)),
      )
    : message.f;
  if (!fields.length && context.matchesOnly) {
    container.insertAdjacentHTML("beforeend", '<div class="tl"><span class="tw"></span><span class="v null">no match</span></div>');
    return;
  }
  if (!message.f.length) {
    container.insertAdjacentHTML("beforeend", '<div class="tl"><span class="tw"></span><span class="v null">{}</span></div>');
    return;
  }

  for (const [name, type, repeated, value] of fields) {
    const isMessage = repeated ? value[0]?.k === "msg" : value.k === "msg";
    const label = fieldLabel(name, repeated && type ? `${type}[]` : type, isMessage, context);
    if (repeated) container.append(listNode(label, value, depth, context));
    else if (value.k === "msg") container.append(messageNode(label, value, depth, context));
    else container.append(scalarLine(label, value, context));
  }
}

export function fieldLabel(name, type, isMessage, context) {
  const typeHtml = type ? `<span class="ty${isMessage ? " msg" : ""}">${escapeHtml(type)}</span>` : "";
  return `${typeHtml}<span class="k">${highlight(name, context.terms)}</span>`;
}

export function scalarLine(label, value, context) {
  const line = document.createElement("div");
  line.className = "tl";
  line.innerHTML = `<span class="tw"></span>${label}<span class="c">:</span>${valueHtml(value, context.terms)}`;
  return line;
}

export function messageNode(label, message, depth, context) {
  const open = depth <= context.openDepth || Boolean(context.matcher?.message(message));
  return collapsible(label, messagePreview(message), open, (children) =>
    appendFields(children, message, depth + 1, context),
  );
}

export function listNode(label, items, depth, context) {
  const scalars = items.length > 0 && items[0].k !== "msg";
  const preview = scalars
    ? escapeHtml(items.slice(0, 10).map(shortValue).join(", ") + (items.length > 10 ? ", …" : ""))
    : "";
  // long lists of numbers read better as the one line preview
  const open =
    (depth <= context.openDepth && items.length <= (scalars ? 10 : 100)) ||
    Boolean(context.matcher && items.some(context.matcher.value));
  return collapsible(`${label}<span class="count">[${items.length}]</span>`, preview, open, (children) => {
    items.forEach((item, index) => {
      // with "matches only" on, the entries that don't match are hidden, but the indexes stay real
      if (context.matchesOnly && context.matcher && !context.matcher.value(item)) return;
      const itemLabel = `<span class="k idx">[${index}]</span>`;
      children.append(
        item.k === "msg" ? messageNode(itemLabel, item, depth + 1, context) : scalarLine(itemLabel, item, context),
      );
    });
  });
}

// children are only built when a node is opened for the first time
export function collapsible(label, preview, open, build) {
  const node = document.createElement("div");
  node.className = "tn";
  node.innerHTML = `<div class="tl x"><span class="tw"></span>${label}<span class="pv">${preview}</span></div><div class="tc"></div>`;
  node._build = build;
  if (open) setNodeOpen(node, true);
  return node;
}

export function setNodeOpen(node, open) {
  if (open && node._build) {
    const build = node._build;
    node._build = null;
    build(node.lastElementChild);
  }
  node.classList.toggle("open", open);
}

export function setNodeOpenDeep(node, open) {
  setNodeOpen(node, open);
  for (const child of node.lastElementChild.children) {
    if (child.classList.contains("tn")) setNodeOpenDeep(child, open);
  }
}

export function valueHtml(value, terms) {
  const text = (content) => highlight(content, terms);
  switch (value.k) {
    case "str":
      return `<span class="v str">"${text(value.v)}"</span>`;
    case "bytes":
      return `<span class="v str">"${text(value.v)}"</span><span class="note">${plural(value.n, "byte")}</span>`;
    case "enum":
      return value.e == null
        ? `<span class="v enum">${text(value.v)}</span>`
        : `<span class="v enum">${text(value.e)}</span><span class="note">${value.v}</span>`;
    case "bool":
      return `<span class="v bool">${value.v}</span>`;
    case "null":
      return '<span class="v null">null</span>';
    default:
      return `<span class="v num">${text(value.v)}</span>`;
  }
}

export function shortValue(value) {
  switch (value.k) {
    case "str":
    case "bytes":
      return JSON.stringify(value.v.length > 32 ? `${value.v.slice(0, 32)}…` : value.v);
    case "enum":
      return value.e ?? String(value.v);
    case "msg":
      return "{…}";
    default:
      return String(value.v);
  }
}

export function messagePreview(message) {
  const parts = [];
  let length = 0;
  for (const [name, , repeated, value] of message.f) {
    if (length > 100) {
      parts.push("…");
      break;
    }
    const part = `${name}: ${repeated ? `[${value.length}]` : shortValue(value)}`;
    parts.push(part);
    length += part.length + 2;
  }
  return escapeHtml(parts.length ? `{ ${parts.join(", ")} }` : "{}");
}

// tells which nodes contain a search match, so they can be opened
export function makeMatcher(terms) {
  if (!terms.length) return null;

  const test = (text) => terms.some((term) => term.test.test(text));
  const memo = new WeakMap();
  const value = (node) =>
    node.k === "msg" ? message(node) : test(node.k === "enum" ? `${node.e ?? ""} ${node.v}` : String(node.v));
  const message = (node) => {
    let found = memo.get(node);
    if (found === undefined) {
      found = node.f.some(([name, , repeated, child]) => test(name) || (repeated ? child.some(value) : value(child)));
      memo.set(node, found);
    }
    return found;
  };
  return { value, message, text: test };
}

export function jsonToNode(value) {
  if (Array.isArray(value)) {
    return { k: "msg", t: "", f: value.map((item, index) => [String(index), "", false, jsonToNode(item)]) };
  }
  if (value !== null && typeof value === "object") {
    return {
      k: "msg",
      t: "",
      f: Object.entries(value).map(([key, item]) =>
        Array.isArray(item) ? [key, "", true, item.map(jsonToNode)] : [key, "", false, jsonToNode(item)],
      ),
    };
  }
  if (typeof value === "string") return { k: "str", v: value };
  if (typeof value === "boolean") return { k: "bool", v: value };
  if (value === null) return { k: "null", v: null };
  return { k: "num", v: value };
}

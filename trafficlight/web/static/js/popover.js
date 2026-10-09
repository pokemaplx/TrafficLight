// @ts-check
import { ui } from "./ui.js";
import { clamp } from "./util.js";

export function openPopover(popover, anchor) {
  closePopovers();
  popover.hidden = false;
  popover._anchor = anchor;
  const rect = anchor.getBoundingClientRect();
  // below the anchor, or above it if it sits at the bottom of the window
  const fitsBelow = rect.bottom + 6 + popover.offsetHeight <= window.innerHeight - 8;
  popover.style.top = `${fitsBelow ? rect.bottom + 6 : Math.max(8, rect.top - 6 - popover.offsetHeight)}px`;
  popover.style.left = `${clamp(rect.left, 8, window.innerWidth - popover.offsetWidth - 8)}px`;
}

export function closePopovers() {
  let closed = false;
  for (const popover of [ui.methodsPop, ui.helpPop, ui.exportPop, ui.clearPop]) {
    if (!popover.hidden) {
      popover.hidden = true;
      closed = true;
    }
  }
  return closed;
}

export function toggleHelp(anchor = ui.helpBtn) {
  if (ui.helpPop.hidden) openPopover(ui.helpPop, anchor);
  else ui.helpPop.hidden = true;
}

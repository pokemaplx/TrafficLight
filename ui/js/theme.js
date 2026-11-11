// @ts-check
import { THEMES, THEME_NAMES } from "./constants.js";
import { prefs, savePrefs } from "./prefs.js";
import { toast, ui } from "./ui.js";

const lightScheme = matchMedia("(prefers-color-scheme: light)");
export function applyTheme() {
  document.documentElement.dataset.theme =
    prefs.theme === "system" ? (lightScheme.matches ? "light" : "dark") : prefs.theme;
  ui.themeBtn.dataset.mode = prefs.theme;
  ui.themeBtn.title = `${THEME_NAMES[prefs.theme]}. Click to switch`;
}
export function switchTheme() {
  const before = document.documentElement.dataset.theme;
  prefs.theme = THEMES[(THEMES.indexOf(prefs.theme) + 1) % THEMES.length];
  savePrefs();
  toast(THEME_NAMES[prefs.theme]);

  const theme = prefs.theme === "system" ? (lightScheme.matches ? "light" : "dark") : prefs.theme;
  const animate =
    document.startViewTransition && theme !== before && !matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (!animate) {
    applyTheme();
    return;
  }

  // the new colors spread out from the button in a circle
  const button = ui.themeBtn.getBoundingClientRect();
  const keyframes = revealKeyframes(button.left + button.width / 2, button.top + button.height / 2);
  document.startViewTransition(applyTheme).ready.then(() => {
    // eases the uncovered area, not the radius: quick at first, and it still moves at the end
    document.documentElement.animate(keyframes, {
      duration: 320,
      easing: "ease-out",
      pseudoElement: "::view-transition-new(root)",
    });
  });
}

// A circle that uncovers the same share of the page in every moment. Easing its radius instead
// leaves the far corner for last, and the edge crawls over it while the rest is long done
export function revealKeyframes(x, y) {
  const columns = 64;
  const rows = 36;
  const distances = [];
  for (let column = 0; column < columns; column++) {
    for (let row = 0; row < rows; row++) {
      distances.push(Math.hypot(((column + 0.5) / columns) * innerWidth - x, ((row + 0.5) / rows) * innerHeight - y));
    }
  }
  distances.sort((a, b) => a - b);

  const farthest = Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y));
  const steps = 30;
  return Array.from({ length: steps + 1 }, (_, step) => {
    const radius =
      step === 0 ? 0 : step === steps ? farthest : distances[Math.round((step / steps) * (distances.length - 1))];
    return { clipPath: `circle(${radius.toFixed(1)}px at ${x}px ${y}px)` };
  });
}

// following the system theme means reacting when it changes
lightScheme.addEventListener("change", applyTheme);

// Preserve the operator's place, never the evidence being refreshed.
(() => {
  let state = null;
  const identity = (element) => JSON.stringify([
    element.id, element.dataset.workSystem, element.dataset.workId, element.dataset.preserveScroll,
  ]);
  document.addEventListener("htmx:beforeSwap", (event) => {
    const panel = event.detail.target;
    if (panel?.id !== "instance-panel") return;
    const active = document.activeElement;
    const disclosure = active?.closest("details[id]");
    state = {
      instance: panel.dataset.instance,
      work: panel.dataset.work,
      open: new Set(Array.from(panel.querySelectorAll("details[id][open]"), identity)),
      scroll: new Map(Array.from(panel.querySelectorAll("[id][data-preserve-scroll]"),
        (element) => [identity(element), [element.scrollLeft, element.scrollTop]])),
      focused: panel.contains(active) ? {
        id: active.id,
        summary: active.matches("summary") && disclosure ? identity(disclosure) : null,
        href: active.getAttribute("href"),
      } : null,
      x: window.scrollX,
      y: window.scrollY,
    };
  });
  document.addEventListener("htmx:afterSwap", (event) => {
    if (event.detail.target?.id !== "instance-panel" || !state) return;
    // With outerHTML, detail.target is the detached old panel.
    const panel = document.getElementById("instance-panel");
    if (!panel || panel.dataset.instance !== state.instance || panel.dataset.work !== state.work) return;
    let focus = null;
    for (const disclosure of panel.querySelectorAll("details[id]")) {
      const key = identity(disclosure);
      disclosure.open = state.open.has(key);
      if (state.focused?.summary === key) focus = disclosure.querySelector("summary");
    }
    if (state.focused?.id) focus = document.getElementById(state.focused.id);
    if (!focus && state.focused?.href) {
      focus = Array.from(panel.querySelectorAll("a[href]"))
        .find((link) => link.getAttribute("href") === state.focused.href);
    }
    focus?.focus({ preventScroll: true });
    for (const element of panel.querySelectorAll("[id][data-preserve-scroll]")) {
      const position = state.scroll.get(identity(element));
      if (position) {
        element.scrollLeft = position[0];
        element.scrollTop = position[1];
      }
    }
    window.scrollTo(state.x, state.y);
  });
})();

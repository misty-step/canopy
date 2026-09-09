// Preserve disclosure state, not its contents: refreshed evidence must replace
// stale evidence even while an operator is reading an expanded ticket.
(() => {
  let open = new Set();
  let focused = null;
  const identity = (element) => JSON.stringify([element.dataset.workSystem, element.dataset.workId]);
  document.addEventListener("htmx:beforeSwap", (event) => {
    if (event.detail.target?.id !== "instance-panel") return;
    const target = event.detail.target;
    open = new Set(Array.from(target.querySelectorAll("details.ticket-evidence[open]"), identity));
    const active = document.activeElement;
    focused = active?.matches("details.ticket-evidence > summary") ? identity(active.parentElement) : null;
  });
  document.addEventListener("htmx:afterSwap", (event) => {
    if (event.detail.target?.id !== "instance-panel") return;
    for (const disclosure of event.detail.target.querySelectorAll("details.ticket-evidence")) {
      const key = identity(disclosure);
      disclosure.open = open.has(key);
      if (focused === key) disclosure.querySelector("summary").focus({ preventScroll: true });
    }
  });
})();

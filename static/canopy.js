// Preserve the operator's place, never the evidence being refreshed.
(() => {
  const snapshots = new Map();
  const filters = new Map([
    ["all", null], ["attention", "needsAttention"],
    ["progress", "inProgress"], ["merged", "merged"],
  ]);
  const searchSelector = '#work-search[type="search"]';
  const identity = (element) => JSON.stringify([
    element.id, element.dataset.workSystem, element.dataset.workId, element.dataset.preserveScroll,
  ]);
  let view;

  function setContext(url, filter, query) {
    if (filter === "all") url.searchParams.delete("filter");
    else url.searchParams.set("filter", filter);
    if (query) url.searchParams.set("q", query);
    else url.searchParams.delete("q");
  }

  function readLocation() {
    const url = new URL(window.location.href);
    const filter = url.searchParams.get("filter");
    view = {
      instance: url.searchParams.get("instance")
        || document.getElementById("instance-panel")?.dataset.instance
        || document.getElementById("fleet")?.dataset.instance || "",
      filter: filters.has(filter) ? filter : "all",
      query: url.searchParams.get("q") || "",
    };
    renderWork();
  }

  function writeLocation() {
    const url = new URL(window.location.href);
    setContext(url, view.filter, view.query);
    if (url.href !== window.location.href) {
      window.history.replaceState(window.history.state, "", url.href);
    }
  }

  function renderWork() {
    const panel = document.getElementById("instance-panel");
    if (!panel || panel.dataset.instance !== view.instance) return;
    const search = panel.querySelector(searchSelector);
    if (search && search.value !== view.query) search.value = view.query;
    const query = view.query.trim().toLowerCase();
    const flag = filters.get(view.filter);
    const rows = panel.querySelectorAll("#work-list [data-work-row]");
    let shown = 0;
    for (const row of rows) {
      const matchesQuery = !query
        || (row.dataset.workTitle || "").toLowerCase().includes(query)
        || (row.dataset.workKey || "").toLowerCase().includes(query)
        || (row.dataset.workId || "").toLowerCase().includes(query);
      row.hidden = !matchesQuery || (flag !== null && row.dataset[flag] !== "true");
      if (!row.hidden) shown++;
    }
    for (const button of panel.querySelectorAll("button[data-work-filter]")) {
      button.setAttribute("aria-pressed", String(button.dataset.workFilter === view.filter));
    }
    for (const status of panel.querySelectorAll("[data-work-results]")) {
      const message = `${shown} of ${rows.length} work ${rows.length === 1 ? "item" : "items"} shown`;
      if (status.textContent !== message) status.textContent = message;
      status.hidden = false;
    }
    for (const empty of panel.querySelectorAll("[data-work-empty]")) {
      empty.hidden = rows.length === 0 || shown !== 0;
    }
    for (const controls of panel.querySelectorAll("[data-work-controls]")) controls.hidden = false;
    for (const link of panel.querySelectorAll("a[data-work-navigation][href]")) {
      const url = new URL(link.href);
      if (url.origin !== window.location.origin) continue;
      const sameInstance = !url.searchParams.has("instance")
        || url.searchParams.get("instance") === view.instance;
      setContext(url, sameInstance ? view.filter : "all", sameInstance ? view.query : "");
      if (link.href !== url.href) link.href = url.href;
    }
  }

  function updateView() {
    writeLocation();
    renderWork();
  }

  function captureReceiptSelection(root) {
    const receipt = root.querySelector("#review-summary");
    const text = receipt?.firstChild;
    const selection = window.getSelection();
    const active = document.activeElement;
    if (!selection || selection.rangeCount !== 1 || selection.isCollapsed
      || !text || text.nodeType !== Node.TEXT_NODE || receipt.childNodes.length !== 1
      || !receipt.contains(selection.anchorNode) || !receipt.contains(selection.focusNode)
      || active?.matches("input, textarea, select") || active?.isContentEditable) return null;
    return {
      identity: identity(receipt),
      // Compare the raw text after the swap; never reinsert the old receipt.
      text: text.data,
      anchor: selection.anchorNode === receipt
        ? (selection.anchorOffset === 0 ? 0 : text.length) : selection.anchorOffset,
      focus: selection.focusNode === receipt
        ? (selection.focusOffset === 0 ? 0 : text.length) : selection.focusOffset,
    };
  }

  function capture(root) {
    const active = document.activeElement;
    const disclosure = active?.closest("details[id]");
    const href = active?.getAttribute("href");
    return {
      instance: root.dataset.instance,
      work: root.dataset.work,
      disclosures: new Map(Array.from(root.querySelectorAll("details[id]"),
        (element) => [identity(element), element.open])),
      scroll: new Map(Array.from(root.querySelectorAll("[id][data-preserve-scroll]"),
        (element) => [identity(element), [element.scrollLeft, element.scrollTop]])),
      receiptSelection: captureReceiptSelection(root),
      focused: root.contains(active) ? {
        id: active.id,
        disclosure: disclosure && root.contains(disclosure) ? identity(disclosure) : null,
        summary: active.matches("summary"),
        filter: active.dataset.workFilter,
        reset: active.matches("button[data-work-reset]"),
        href,
        linkIndex: href === null ? -1 : Array.from(root.querySelectorAll("a[href]"))
          .filter((link) => link.getAttribute("href") === href).indexOf(active),
        selection: active.matches(searchSelector)
          ? [active.selectionStart, active.selectionEnd, active.selectionDirection] : null,
      } : null,
      x: window.scrollX,
      y: window.scrollY,
    };
  }

  function restore(root, state) {
    if (!state) return;
    if (root.id === "instance-panel"
      && (root.dataset.instance !== state.instance || root.dataset.work !== state.work)) return;
    const focused = state.focused;
    let summary = null;
    for (const disclosure of root.querySelectorAll("details[id]")) {
      const key = identity(disclosure);
      if (state.disclosures.has(key)) disclosure.open = state.disclosures.get(key);
      if (focused?.disclosure === key) summary = disclosure.querySelector("summary");
    }
    const active = document.activeElement;
    const canRestoreFocus = active === document.body || root.contains(active);
    if (focused && canRestoreFocus) {
      let focus = focused.id ? document.getElementById(focused.id) : null;
      if (focus && !root.contains(focus)) focus = null;
      if (!focus && focused.summary) focus = summary;
      if (!focus && focused.filter !== undefined) {
        focus = Array.from(root.querySelectorAll("button[data-work-filter]"))
          .find((button) => button.dataset.workFilter === focused.filter);
      }
      if (!focus && focused.reset) focus = root.querySelector("button[data-work-reset]");
      if (!focus && focused.href !== null) {
        focus = Array.from(root.querySelectorAll("a[href]"))
          .filter((link) => link.getAttribute("href") === focused.href)[focused.linkIndex];
      }
      if (!focus || !focus.getClientRects().length || focus.closest("[hidden]")) {
        focus = summary || root.querySelector(searchSelector);
      }
      if (focus && focus.getClientRects().length && !focus.closest("[hidden]")) {
        focus.focus({ preventScroll: true });
        if (focused.selection && focus.matches(searchSelector)) {
          focus.setSelectionRange(...focused.selection);
        }
      }
    }
    const receiptSelection = state.receiptSelection;
    if (receiptSelection && canRestoreFocus) {
      const receipt = root.querySelector("#review-summary");
      const text = receipt?.firstChild;
      if (text?.nodeType === Node.TEXT_NODE && receipt.childNodes.length === 1
        && identity(receipt) === receiptSelection.identity && text.data === receiptSelection.text) {
        window.getSelection()?.setBaseAndExtent(
          text, receiptSelection.anchor, text, receiptSelection.focus,
        );
      }
    }
    for (const element of root.querySelectorAll("[id][data-preserve-scroll]")) {
      // A changed receipt SHA has a new identity, so its position starts at zero.
      const position = state.scroll.get(identity(element));
      element.scrollLeft = position ? position[0] : 0;
      element.scrollTop = position ? position[1] : 0;
    }
    window.scrollTo(state.x, state.y);
  }

  function dismissSwitcher(switcher) {
    const returnFocus = switcher.contains(document.activeElement);
    switcher.open = false;
    if (returnFocus) switcher.querySelector("summary")?.focus({ preventScroll: true });
  }

  function openLinkedDetails(link) {
    if (link.origin !== window.location.origin || link.pathname !== window.location.pathname
      || link.search !== window.location.search || !link.hash) return;
    let id;
    try {
      id = decodeURIComponent(link.hash.slice(1));
    } catch {
      return;
    }
    let disclosure = document.getElementById(id);
    if (!disclosure?.matches("details")) return;
    while (disclosure) {
      disclosure.open = true;
      disclosure = disclosure.parentElement?.closest("details");
    }
  }

  document.addEventListener("htmx:beforeSwap", (event) => {
    const root = event.detail.target;
    if (!["instance-panel", "fleet"].includes(root?.id) || event.detail.shouldSwap === false) return;
    if (root.id === "instance-panel" && root.dataset.instance === view.instance) {
      const search = root.querySelector(searchSelector);
      if (search && search.value !== view.query) {
        view.query = search.value;
        writeLocation();
      }
    }
    snapshots.set(root.id, capture(root));
  });

  document.addEventListener("htmx:afterSwap", (event) => {
    const id = event.detail.target?.id;
    if (id !== "instance-panel" && id !== "fleet") return;
    // outerHTML leaves detail.target pointing to the detached old root.
    const root = document.getElementById(id);
    const state = snapshots.get(id);
    snapshots.delete(id);
    if (!root) return;
    if (id === "instance-panel") {
      if (root.dataset.instance !== view.instance) {
        view = { instance: root.dataset.instance, filter: "all", query: "" };
        const instance = new URL(window.location.href).searchParams.get("instance");
        if (!instance || instance === view.instance) writeLocation();
      }
      renderWork();
    }
    restore(root, state);
  });

  document.addEventListener("input", (event) => {
    if (!event.target.matches(searchSelector) || !event.target.closest("#instance-panel")) return;
    view.query = event.target.value;
    updateView();
  });

  document.addEventListener("click", (event) => {
    const target = event.target;
    const link = target.closest("a[href]");
    const navigation = link && event.button === 0
      && !event.metaKey && !event.ctrlKey && !event.altKey && !event.shiftKey
      && (!link.target || link.target === "_self") && !link.hasAttribute("download");
    const switcher = document.getElementById("fleet-switcher");
    if (switcher?.open) {
      if (!switcher.contains(target) || (navigation && switcher.contains(link))) {
        dismissSwitcher(switcher);
      }
    }
    if (navigation && !event.defaultPrevented) openLinkedDetails(link);
    const button = target.closest("button[data-work-filter], button[data-work-reset]");
    if (!button || !button.closest("#instance-panel")) return;
    const reset = button.hasAttribute("data-work-reset");
    if (!reset && !filters.has(button.dataset.workFilter)) return;
    event.preventDefault();
    view.filter = reset ? "all" : button.dataset.workFilter;
    if (reset) view.query = "";
    updateView();
    if (reset) document.querySelector(searchSelector)?.focus({ preventScroll: true });
  });

  document.addEventListener("keydown", (event) => {
    if (event.defaultPrevented || event.isComposing || event.metaKey || event.ctrlKey || event.altKey) return;
    const target = event.target;
    const search = document.querySelector(searchSelector);
    if (event.key === "Escape") {
      if (target === search && search.value) {
        event.preventDefault();
        view.query = "";
        updateView();
        return;
      }
      const switcher = document.getElementById("fleet-switcher");
      if (switcher?.open) {
        event.preventDefault();
        dismissSwitcher(switcher);
      }
    } else if (event.key === "/" && !event.shiftKey && search
      && !target.isContentEditable && !target.closest('input, textarea, select, [role="textbox"]')
      && search.getClientRects().length && !search.closest("[hidden]")) {
      event.preventDefault();
      search.focus();
    }
  });

  window.addEventListener("popstate", readLocation);
  window.addEventListener("pageshow", () => {
    snapshots.clear();
    readLocation();
  });
  readLocation();
})();

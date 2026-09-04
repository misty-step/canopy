(function () {
  "use strict";

  var lastTrigger = null;

  function drawer() {
    return document.getElementById("log-drawer");
  }

  function body() {
    return document.getElementById("log-drawer-body");
  }

  function openDrawer(trigger) {
    var panel = drawer();
    if (!panel) {
      return;
    }
    var content = body();
    if (content) {
      content.innerHTML = '<p class="muted">Loading log\u2026</p>';
    }
    lastTrigger = trigger || null;
    panel.hidden = false;
    var closeButton = panel.querySelector(".log-drawer-close");
    if (closeButton) {
      closeButton.focus();
    }
  }

  function closeDrawer() {
    var panel = drawer();
    if (!panel) {
      return;
    }
    panel.hidden = true;
    var content = body();
    if (content) {
      content.innerHTML = "";
    }
    var restore = lastTrigger && document.contains(lastTrigger)
      ? lastTrigger
      : document.getElementById("main-content");
    if (restore && typeof restore.focus === "function") {
      restore.focus();
    }
    lastTrigger = null;
  }

  function copyLog(button) {
    var pane = button.closest(".log-pane");
    var text = pane ? pane.querySelector(".log-text") : null;
    var value = text ? text.textContent : "";
    copyText(value).then(function (copied) {
      if (!copied) {
        return;
      }
      var label = button.textContent;
      button.textContent = "Copied";
      button.disabled = true;
      window.setTimeout(function () {
        button.textContent = label;
        button.disabled = false;
      }, 1200);
    });
  }

  function copyText(value) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(value).then(
        function () { return true; },
        function () { return legacyCopy(value); }
      );
    }
    return Promise.resolve(legacyCopy(value));
  }

  function legacyCopy(value) {
    var textarea = document.createElement("textarea");
    textarea.value = value;
    textarea.setAttribute("readonly", "");
    textarea.style.position = "fixed";
    textarea.style.top = "-9999px";
    document.body.appendChild(textarea);
    var selection = document.getSelection();
    var range = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null;
    textarea.select();
    textarea.setSelectionRange(0, textarea.value.length);
    var copied = false;
    try {
      copied = document.execCommand("copy");
    } catch (err) {
      copied = false;
    }
    document.body.removeChild(textarea);
    if (range && selection) {
      selection.removeAllRanges();
      selection.addRange(range);
    }
    return copied;
  }

  document.addEventListener("click", function (event) {
    var openLink = event.target.closest("[data-log-open]");
    if (openLink) {
      if (event.button === 0 && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) {
        openDrawer(openLink);
      }
      return;
    }
    if (event.target.closest("[data-log-close]")) {
      closeDrawer();
      return;
    }
    var copyButton = event.target.closest("[data-log-copy]");
    if (copyButton) {
      copyLog(copyButton);
    }
  });

  document.addEventListener("keydown", function (event) {
    var panel = drawer();
    if (!panel || panel.hidden) {
      return;
    }
    if (event.key === "Escape") {
      closeDrawer();
      return;
    }
    if (event.key === "Tab") {
      var focusable = panel.querySelectorAll(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
      );
      if (!focusable.length) {
        return;
      }
      var first = focusable[0];
      var last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    }
  });
})();

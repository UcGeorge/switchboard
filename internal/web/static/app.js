/* Switchboard dashboard: theme, live updates over SSE, small UI helpers. */
(function () {
  "use strict";

  // theme
  var root = document.documentElement;
  function applyTheme(t) {
    if (t === "light" || t === "dark") root.setAttribute("data-theme", t);
    else root.removeAttribute("data-theme");
    document.querySelectorAll("[data-theme-toggle]").forEach(function (b) {
      b.setAttribute("aria-pressed", t === "light" ? "true" : "false");
    });
  }
  try { applyTheme(localStorage.getItem("sb-theme") || ""); } catch (e) {}
  if (!root.hasAttribute("data-theme") && window.matchMedia("(prefers-color-scheme: light)").matches) {
    root.setAttribute("data-theme", "light");
  }
  document.addEventListener("click", function (ev) {
    var t = ev.target.closest("[data-theme-toggle]");
    if (!t) return;
    var next = root.getAttribute("data-theme") === "light" ? "dark" : "light";
    applyTheme(next);
    try { localStorage.setItem("sb-theme", next); } catch (e) {}
  });

  // htmx config: CSRF header, errors
  var csrf = (document.querySelector('meta[name="csrf"]') || {}).content || "";
  document.addEventListener("htmx:configRequest", function (ev) {
    if (csrf) ev.detail.headers["X-CSRF-Token"] = csrf;
  });
  document.addEventListener("htmx:responseError", function (ev) {
    var x = ev.detail.xhr;
    if (x && x.status === 401) { window.location.href = "/login?next=" + encodeURIComponent(location.pathname + location.search); return; }
    toast((x && x.responseText && x.responseText.length < 300) ? x.responseText : "Request failed (" + (x ? x.status : "?") + ")", "err");
  });
  document.addEventListener("htmx:beforeSwap", function (ev) {
    // Allow 4xx responses that carry HTML (validation errors in modals).
    var s = ev.detail.xhr.status;
    if (s >= 400 && s < 500 && /text\/html/.test(ev.detail.xhr.getResponseHeader("Content-Type") || "")) {
      ev.detail.shouldSwap = true; ev.detail.isError = false;
    }
  });

  // toasts
  var toastHost;
  function toast(msg, kind) {
    if (!toastHost) {
      toastHost = document.createElement("div");
      toastHost.style.cssText = "position:fixed;right:18px;bottom:18px;z-index:100;display:flex;flex-direction:column;gap:8px;max-width:360px";
      document.body.appendChild(toastHost);
    }
    var el = document.createElement("div");
    el.className = "flash " + (kind || "info");
    el.style.boxShadow = "var(--shadow)";
    el.textContent = msg;
    toastHost.appendChild(el);
    setTimeout(function () { el.style.opacity = "0"; el.style.transition = "opacity .3s"; setTimeout(function () { el.remove(); }, 320); }, 3200);
  }
  window.sbToast = toast;
  document.addEventListener("htmx:afterRequest", function (ev) {
    var x = ev.detail.xhr; if (!x) return;
    var m = x.getResponseHeader("X-Toast");
    if (m) toast(decodeURIComponent(m), x.getResponseHeader("X-Toast-Kind") || "ok");
  });

  // modal
  function closeModal() { var m = document.getElementById("modal"); if (m) m.innerHTML = ""; }
  window.sbCloseModal = closeModal;
  document.addEventListener("click", function (ev) {
    if (ev.target.closest("[data-close-modal]")) { ev.preventDefault(); closeModal(); }
    else if (ev.target.classList && ev.target.classList.contains("modal-backdrop")) closeModal();
  });
  document.addEventListener("keydown", function (ev) { if (ev.key === "Escape") closeModal(); });
  document.addEventListener("htmx:afterRequest", function (ev) {
    var x = ev.detail.xhr;
    if (x && x.getResponseHeader("X-Close-Modal")) closeModal();
  });

  // copy buttons
  document.addEventListener("click", function (ev) {
    var b = ev.target.closest("[data-copy]");
    if (!b) return;
    ev.preventDefault();
    var text = b.getAttribute("data-copy");
    if (text === "" || text === null) {
      var sel = b.getAttribute("data-copy-target");
      var src = sel ? document.querySelector(sel) : b.parentElement.querySelector("code, pre");
      text = src ? src.innerText : "";
    }
    navigator.clipboard.writeText(text).then(function () {
      var old = b.innerHTML; b.innerHTML = "Copied"; setTimeout(function () { b.innerHTML = old; }, 1200);
    }, function () { toast("Copy failed — select the text manually", "err"); });
  });

  // tabs
  document.addEventListener("click", function (ev) {
    var t = ev.target.closest("[data-tab]");
    if (!t) return;
    var group = t.closest("[data-tabs]");
    if (!group) return;
    group.querySelectorAll("[data-tab]").forEach(function (b) { b.classList.toggle("active", b === t); });
    group.querySelectorAll("[data-panel]").forEach(function (p) { p.classList.toggle("active", p.getAttribute("data-panel") === t.getAttribute("data-tab")); });
  });

  // relative times
  function rel(ms) {
    var d = Date.now() - ms, s = Math.round(d / 1000);
    if (s < 5) return "just now";
    if (s < 60) return s + "s ago";
    var m = Math.round(s / 60); if (m < 60) return m + "m ago";
    var h = Math.round(m / 60); if (h < 48) return h + "h ago";
    return Math.round(h / 24) + "d ago";
  }
  function tickTimes() {
    document.querySelectorAll("[data-ts]").forEach(function (el) {
      var ms = +el.getAttribute("data-ts"); if (ms) el.textContent = rel(ms);
    });
    document.querySelectorAll("[data-since]").forEach(function (el) {
      var ms = +el.getAttribute("data-since"); if (!ms) return;
      var d = Date.now() - ms; el.textContent = d < 1000 ? d + "ms" : (d / 1000).toFixed(d < 10000 ? 1 : 0) + "s";
    });
  }
  setInterval(tickTimes, 1000);
  document.addEventListener("htmx:afterSettle", tickTimes);

  // live updates (SSE)
  // Kinds map to body events that htmx triggers listen for
  // (hx-trigger="sb:requests from:body"), plus per-request delta streaming.
  var dot = document.getElementById("live-indicator");
  function setLive(ok) { if (dot) dot.classList.toggle("off", !ok); }
  var fire = (function () {
    var pending = {};
    return function (name) {
      if (pending[name]) return;
      pending[name] = setTimeout(function () { delete pending[name]; document.body.dispatchEvent(new CustomEvent(name)); }, 400);
    };
  })();
  // The stream is held only while the page is visible. Browsers allow six
  // HTTP/1.1 connections per host, so a stream per background tab (or per
  // page parked in the back/forward cache) would starve every other request
  // to the dashboard.
  var es = null, missed = false;
  // Fire every sb:* event this page listens for, to catch up after a gap.
  function resync() {
    var names = {};
    document.querySelectorAll("[hx-trigger]").forEach(function (el) {
      (el.getAttribute("hx-trigger").match(/sb:[\w:.-]+/g) || []).forEach(function (n) { names[n] = true; });
    });
    Object.keys(names).forEach(fire);
  }
  function disconnect() {
    if (!es) return;
    es.close(); es = null; missed = true; setLive(false);
  }
  function connect() {
    if (es || !window.EventSource || document.body.getAttribute("data-live") !== "1") return;
    es = new EventSource("/live");
    es.onopen = function () { setLive(true); if (missed) { missed = false; resync(); } };
    es.onerror = function () { setLive(false); missed = true; };
    es.addEventListener("event", function (ev) {
      var e; try { e = JSON.parse(ev.data); } catch (err) { return; }
      var k = e.kind || "";
      if (k === "request.delta") {
        var out = document.querySelector('[data-live-output="' + e.request_id + '"]');
        if (out && e.data && typeof e.data.text === "string") {
          var ph = out.querySelector(".placeholder"); if (ph) ph.remove();
          var cur = out.querySelector(".cursor"); if (cur) cur.remove();
          out.appendChild(document.createTextNode(e.data.text));
          var c = document.createElement("span"); c.className = "cursor"; out.appendChild(c);
          if (out.hasAttribute("data-autoscroll")) out.scrollTop = out.scrollHeight;
        }
        return;
      }
      if (k.indexOf("request.") === 0) {
        fire("sb:requests"); fire("sb:stats");
        if (e.request_id) fire("sb:request:" + e.request_id);
        if (e.conversation_id) fire("sb:conversation:" + e.conversation_id);
        if (e.channel_id) fire("sb:channel:" + e.channel_id);
      } else if (k.indexOf("channel.") === 0) {
        fire("sb:channels"); fire("sb:stats"); fire("sb:requests");
        if (e.channel_id) fire("sb:channel:" + e.channel_id);
      } else if (k.indexOf("key.") === 0) {
        fire("sb:keys");
      } else if (k.indexOf("token.") === 0 || k.indexOf("oauth.") === 0) {
        fire("sb:agents");
      }
      fire("sb:events");
    });
  }
  function syncStream() {
    if (document.visibilityState === "visible") connect(); else disconnect();
  }
  document.addEventListener("visibilitychange", syncStream);
  window.addEventListener("pagehide", disconnect);
  window.addEventListener("pageshow", syncStream);
  syncStream();

  // confirm dialogs for destructive htmx actions
  document.addEventListener("htmx:confirm", function (ev) {
    var q = ev.detail.elt.getAttribute("data-confirm");
    if (!q) return;
    ev.preventDefault();
    if (window.confirm(q)) ev.detail.issueRequest();
  });

  // auto-focus first input in modals
  document.addEventListener("htmx:afterSettle", function (ev) {
    if (ev.target && ev.target.id === "modal") {
      var f = ev.target.querySelector("input:not([type=hidden]), textarea, select");
      if (f) f.focus();
    }
  });
})();

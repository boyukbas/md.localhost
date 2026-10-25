/* md.localhost viewer behaviour --------------------------------------- */
(function () {
  "use strict";

  var root = document.documentElement;
  var THEME_KEY = "mdl.theme";

  /* ---- theme -------------------------------------------------------- */

  function storedTheme() {
    try { return localStorage.getItem(THEME_KEY) || "auto"; } catch (e) { return "auto"; }
  }
  function storeTheme(v) {
    try { localStorage.setItem(THEME_KEY, v); } catch (e) { /* private mode */ }
  }
  function prefersDark() {
    return window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
  }
  function isDark() {
    var t = storedTheme();
    return t === "dark" || (t === "auto" && prefersDark());
  }
  function applyTheme() {
    root.classList.toggle("theme-dark", isDark());
    root.setAttribute("data-theme", storedTheme());
  }

  applyTheme();

  if (window.matchMedia) {
    var mq = window.matchMedia("(prefers-color-scheme: dark)");
    var onScheme = function () { if (storedTheme() === "auto") { applyTheme(); renderMermaid(true); } };
    if (mq.addEventListener) { mq.addEventListener("change", onScheme); }
    else if (mq.addListener) { mq.addListener(onScheme); }
  }

  var themeBtn = document.getElementById("theme-toggle");
  if (themeBtn) {
    themeBtn.addEventListener("click", function () {
      var order = ["auto", "light", "dark"];
      var next = order[(order.indexOf(storedTheme()) + 1) % order.length];
      storeTheme(next);
      themeBtn.title = "Theme: " + next;
      applyTheme();
      renderMermaid(true);
    });
    themeBtn.title = "Theme: " + storedTheme();
  }

  var printBtn = document.getElementById("print-btn");
  if (printBtn) { printBtn.addEventListener("click", function () { window.print(); }); }

  /* ---- code blocks -------------------------------------------------- */

  var WRAP_KEY = "mdl.wrap";
  var JIRA_KEY = "mdl.jira";

  function wrapOn() {
    try { return localStorage.getItem(WRAP_KEY) !== "off"; } catch (e) { return true; }
  }
  function jiraPreviewOn() {
    try { return localStorage.getItem(JIRA_KEY) !== "off"; } catch (e) { return true; }
  }

  // The class lives on <html> because page.html sets it before <body> exists,
  // which is what stops a wrapped-then-unwrapped flash on load.
  function applyWrap() {
    var on = wrapOn();
    root.classList.toggle("mdl-nowrap", !on);
    document.querySelectorAll(".wrap-btn").forEach(function (b) {
      b.setAttribute("aria-pressed", on ? "true" : "false");
      b.title = on ? "Long lines wrap. Click to let them scroll instead."
                   : "Long lines scroll. Click to wrap them.";
    });
  }

  function applyJiraPreview() {
    var on = jiraPreviewOn();
    root.classList.toggle("mdl-jira-raw", !on);
    document.querySelectorAll(".jira-btn").forEach(function (b) {
      b.setAttribute("aria-pressed", on ? "true" : "false");
      b.title = on ? "Showing how Jira will render this. Click for the raw markup."
                   : "Showing the raw markup. Click to see how Jira will render it.";
    });
  }

  // The declared fence language, or "" when the block did not name one.
  function langOf(code) {
    var m = /\blanguage-([\w+#-]+)\b/.exec(code.className || "");
    return m ? m[1] : "";
  }

  // Only ever highlight a language the document actually declared. Left to
  // guess, highlight.js scores Jira wiki markup as vbnet or swift with high
  // confidence and paints prose in keyword colours, which is worse than no
  // colour at all — and no relevance threshold separates those false hits
  // from real code.
  function highlight(scope) {
    var hljs = window.__mdlHljs;
    if (!hljs) { return; }
    scope.querySelectorAll("pre > code").forEach(function (block) {
      if (block.dataset.mdlHighlighted) { return; }
      var lang = langOf(block);
      if (lang === "mermaid") { return; }
      if (!lang || !hljs.getLanguage(lang)) { return; }
      try { hljs.highlightElement(block); } catch (e) { /* malformed grammar */ }
      block.dataset.mdlHighlighted = "1";
    });
  }

  // Writing to the clipboard is a permission, and a managed profile can refuse
  // it. Where the Clipboard API exists it is the only mechanism trusted to
  // report honestly: execCommand("copy") returns true and writes nothing when
  // the permission is denied, so a refusal hands over to the keyboard instead
  // of claiming a copy that did not happen. execCommand is kept only for the
  // case the API is absent altogether — an insecure origin, e.g. reaching this
  // server by IP from another machine — where it is the intended mechanism.
  function copyText(btn, code) {
    var text = code.textContent;
    var settle = function (label) {
      btn.textContent = label;
      setTimeout(function () { btn.textContent = "Copy"; }, 1600);
    };

    var selectBlock = function () {
      try {
        var sel = window.getSelection();
        var range = document.createRange();
        range.selectNodeContents(code);
        sel.removeAllRanges();
        sel.addRange(range);
        settle("Ctrl+C");
      } catch (e) {
        settle("Failed");
      }
    };

    var legacy = function () {
      var ta = document.createElement("textarea");
      ta.value = text;
      ta.setAttribute("readonly", "");
      // Parked off to the left rather than hidden: Chrome refuses to copy from
      // a textarea it treats as unrendered, so opacity:0 or a 1px box makes
      // execCommand quietly return false. "top" tracks the scroll position so
      // focusing it cannot jump the page.
      ta.style.cssText = "position:absolute;left:-9999px;top:" + window.pageYOffset +
        "px;width:2em;height:2em;padding:0;margin:0;border:0;font-size:12pt";
      document.body.appendChild(ta);
      ta.select();
      try { ta.setSelectionRange(0, text.length); } catch (e) { /* not selectable */ }
      var ok = false;
      try { ok = document.execCommand("copy"); } catch (e) { ok = false; }
      document.body.removeChild(ta);
      if (ok) { settle("Copied"); } else { selectBlock(); }
    };

    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () { settle("Copied"); }, selectBlock);
      return;
    }
    legacy();
  }

  function button(cls, label) {
    var b = document.createElement("button");
    b.type = "button";
    b.className = "codebtn " + cls;
    b.textContent = label;
    return b;
  }

  // Give every code block a header bar: what it is on the left, wrap and copy
  // on the right. The bar is a sibling of the <pre> rather than a child, so
  // selecting the code by hand never picks up the button captions.
  function decorateCodeBlocks(scope) {
    scope.querySelectorAll("pre").forEach(function (pre) {
      var parent = pre.parentElement;
      if (parent && parent.classList.contains("codeblock")) { return; }
      var code = pre.querySelector("code");
      if (!code) { return; }
      if (langOf(code) === "mermaid") { return; }  // becomes a diagram instead

      var bar = document.createElement("div");
      bar.className = "codebar";

      var name = document.createElement("span");
      name.className = "lang";
      bar.appendChild(name);

      // A block of Jira wiki markup is not code and highlighting it is
      // meaningless, but seeing it the way Jira will draw it is the whole
      // reason for pasting it here. The source stays put; the preview is a
      // sibling, and Copy is wired to the <code> either way.
      var jira = window.__mdlJira;
      var declared = langOf(code);
      var preview = null;
      if (jira && (declared === "jira" || (!declared && jira.looksLikeJira(code.textContent)))) {
        preview = document.createElement("div");
        preview.className = "jira-body";
        try {
          preview.innerHTML = jira.render(code.textContent);
        } catch (e) {
          preview = null;   // a renderer bug must not cost you the source
        }
      }
      name.textContent = preview ? "jira" : (declared || "text");

      if (preview) {
        var jiraBtn = button("jira-btn", "Preview");
        jiraBtn.addEventListener("click", function () {
          try { localStorage.setItem(JIRA_KEY, jiraPreviewOn() ? "off" : "on"); } catch (e) { /* private mode */ }
          applyJiraPreview();
        });
        bar.appendChild(jiraBtn);
      }

      var wrapBtn = button("wrap-btn", "Wrap");
      wrapBtn.addEventListener("click", function () {
        try { localStorage.setItem(WRAP_KEY, wrapOn() ? "off" : "on"); } catch (e) { /* private mode */ }
        applyWrap();
      });
      bar.appendChild(wrapBtn);

      var copyBtn = button("copy-btn", "Copy");
      copyBtn.title = "Copy this block exactly as it is in the file";
      copyBtn.addEventListener("click", function () { copyText(copyBtn, code); });
      bar.appendChild(copyBtn);

      var box = document.createElement("div");
      box.className = preview ? "codeblock jira" : "codeblock";
      pre.replaceWith(box);
      box.appendChild(bar);
      box.appendChild(pre);
      if (preview) { box.appendChild(preview); }
    });
    applyWrap();
    applyJiraPreview();
  }

  /* ---- mermaid ------------------------------------------------------ */

  var mermaidLoading = null;
  var mermaidSeq = 0;

  function loadMermaid() {
    if (window.__mdlMermaid && typeof window.__mdlMermaid.initialize === "function") {
      return Promise.resolve(window.__mdlMermaid);
    }
    if (mermaidLoading) { return mermaidLoading; }
    mermaidLoading = new Promise(function (resolve, reject) {
      var s = document.createElement("script");
      s.src = "/assets/mermaid.min.js";
      s.onload = function () {
        if (window.__mdlMermaid && typeof window.__mdlMermaid.initialize === "function") {
          resolve(window.__mdlMermaid);
        } else {
          reject(new Error("mermaid did not initialise"));
        }
      };
      s.onerror = function () { reject(new Error("could not load mermaid")); };
      document.head.appendChild(s);
    });
    return mermaidLoading;
  }

  // Turn <pre><code class="language-mermaid"> into a container that keeps the
  // original source, so diagrams can be re-rendered when the theme changes.
  function collectMermaid(scope) {
    var found = [];
    scope.querySelectorAll("pre > code.language-mermaid").forEach(function (code) {
      var pre = code.parentElement;
      var wrap = document.createElement("div");
      wrap.className = "mermaid-wrap";
      wrap.dataset.src = code.textContent;
      pre.replaceWith(wrap);
      found.push(wrap);
    });
    scope.querySelectorAll(".mermaid-wrap").forEach(function (w) {
      if (found.indexOf(w) === -1) { found.push(w); }
    });
    return found;
  }

  function renderMermaid(force) {
    var doc = document.getElementById("doc");
    if (!doc) { return; }
    var blocks = collectMermaid(doc);
    if (!blocks.length) { return; }

    loadMermaid().then(function (mermaid) {
      var css = getComputedStyle(document.body);
      mermaid.initialize({
        startOnLoad: false,
        securityLevel: "strict",
        theme: isDark() ? "dark" : "default",
        fontFamily: css.fontFamily,
        themeVariables: {
          // Without this, edge labels sit on an opaque grey chip that clashes
          // with the panel behind the diagram.
          edgeLabelBackground: css.getPropertyValue("--bg-subtle").trim()
        }
      });
      blocks.forEach(function (wrap) {
        if (wrap.dataset.rendered === "1" && !force) { return; }
        mermaidSeq += 1;
        var id = "mmd-" + mermaidSeq;
        mermaid.render(id, wrap.dataset.src).then(function (out) {
          wrap.innerHTML = out.svg;
          wrap.dataset.rendered = "1";
          if (out.bindFunctions) { out.bindFunctions(wrap); }
        }).catch(function (err) {
          wrap.innerHTML = "";
          var p = document.createElement("div");
          p.className = "mermaid-error";
          p.textContent = "Mermaid error: " + (err && err.message ? err.message : err);
          wrap.appendChild(p);
          wrap.dataset.rendered = "1";
        });
      });
    }).catch(function (err) {
      blocks.forEach(function (wrap) {
        wrap.textContent = "Mermaid unavailable: " + err.message;
      });
    });
  }

  /* ---- table of contents -------------------------------------------- */

  function buildToc() {
    var toc = document.getElementById("toc");
    var doc = document.getElementById("doc");
    if (!toc || !doc) { return; }

    var heads = Array.prototype.slice.call(doc.querySelectorAll("h2[id], h3[id]"));
    toc.innerHTML = "";
    if (heads.length < 3) { toc.hidden = true; return; }
    toc.hidden = localStorage.getItem("mdl.toc") === "off";

    var title = document.createElement("div");
    title.className = "toc-title";
    title.textContent = "Contents";
    toc.appendChild(title);

    var links = heads.map(function (h) {
      var a = document.createElement("a");
      a.href = "#" + h.id;
      a.textContent = h.textContent.replace(/¶/g, "").trim();
      a.className = h.tagName === "H3" ? "h3" : "h2";
      toc.appendChild(a);
      return a;
    });

    // Highlight the heading currently nearest the top of the viewport.
    if ("IntersectionObserver" in window) {
      var visible = new Set();
      var obs = new IntersectionObserver(function (entries) {
        entries.forEach(function (e) {
          if (e.isIntersecting) { visible.add(e.target.id); } else { visible.delete(e.target.id); }
        });
        var activeId = null;
        for (var i = 0; i < heads.length; i++) {
          if (visible.has(heads[i].id)) { activeId = heads[i].id; break; }
        }
        links.forEach(function (a) {
          a.classList.toggle("active", activeId !== null && a.getAttribute("href") === "#" + activeId);
        });
      }, { rootMargin: "-60px 0px -70% 0px" });
      heads.forEach(function (h) { obs.observe(h); });
    }
  }

  var tocBtn = document.getElementById("toc-toggle");
  if (tocBtn) {
    tocBtn.addEventListener("click", function () {
      var toc = document.getElementById("toc");
      if (!toc) { return; }
      toc.hidden = !toc.hidden;
      try { localStorage.setItem("mdl.toc", toc.hidden ? "off" : "on"); } catch (e) { /* ignore */ }
    });
  }

  /* ---- viewer identity ----------------------------------------------- */

  // Every browser tab showing md.localhost is a "viewer". The server hands a
  // newly opened document to whichever viewer the user looked at last, so a
  // file double-clicked in Explorer becomes a tab inside the window already on
  // screen instead of yet another browser tab.
  var VIEWER = (function () {
    try {
      var id = sessionStorage.getItem("mdl.viewer");
      if (!id) {
        id = Math.random().toString(36).slice(2) + Date.now().toString(36);
        sessionStorage.setItem("mdl.viewer", id);
      }
      return id;
    } catch (e) {
      return "anon";
    }
  })();

  function post(path, payload) {
    return fetch(path, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    });
  }

  function reportFocus() {
    post("/viewer/focus", { id: VIEWER }).catch(function () { /* server gone */ });
  }

  reportFocus();
  window.addEventListener("focus", reportFocus);
  document.addEventListener("visibilitychange", function () {
    if (!document.hidden) { reportFocus(); }
  });
  // A page the user is sitting on but not clicking is still the page they are
  // reading, and the server drops the least recently active viewer first.
  setInterval(function () { if (!document.hidden) { reportFocus(); } }, 60000);

  /* ---- tab strip ------------------------------------------------------ */

  var strip = document.getElementById("tabs");

  // The server addresses documents by their real path, so the strip mirrors
  // what pathToURLPath does on the Go side.
  function toUrlPath(p) {
    var s = p.replace(/\\/g, "/");
    if (s.indexOf("//") === 0) { return "unc/" + s.slice(2); }
    return s.replace(/^\//, "");
  }

  function renderTabs(tabs) {
    if (!strip) { return; }
    strip.innerHTML = "";
    tabs.forEach(function (t) {
      var a = document.createElement("a");
      a.className = "tab" + (t.active ? " active" : "");
      a.href = t.url;
      a.title = t.path;
      a.dataset.path = t.path;

      var name = document.createElement("span");
      name.className = "tab-name";
      name.textContent = t.name;

      var close = document.createElement("span");
      close.className = "tab-close";
      close.setAttribute("role", "button");
      close.setAttribute("aria-label", "Close " + t.name);
      close.textContent = "\u00d7";

      a.appendChild(name);
      a.appendChild(close);
      strip.appendChild(a);
    });
    document.body.classList.toggle("with-tabs", tabs.length > 0);
    var active = strip.querySelector(".tab.active");
    if (active && active.scrollIntoView) {
      active.scrollIntoView({ block: "nearest", inline: "nearest" });
    }
  }

  function refreshTabs() {
    if (!strip) { return; }
    var path = document.body.getAttribute("data-doc-path") || "";
    var q = path ? "?active=" + encodeURIComponent(toUrlPath(path)) : "";
    fetch("/tabs.json" + q, { credentials: "same-origin", cache: "no-store" })
      .then(function (r) { return r.json(); })
      .then(function (d) { renderTabs(d.tabs || []); })
      .catch(function () { /* the strip already on screen is good enough */ });
  }

  function closeTab(tab) {
    if (!tab) { return; }
    var wasActive = tab.classList.contains("active");
    post("/tabs/close", { path: tab.dataset.path })
      .then(function (r) { return r.json(); })
      .then(function (d) {
        if (wasActive) { window.location.assign(d.next || "/"); return; }
        tab.remove();
      })
      .catch(function () { /* leave the strip alone */ });
  }

  if (strip) {
    strip.addEventListener("click", function (e) {
      var btn = e.target.closest(".tab-close");
      if (!btn) { return; }
      e.preventDefault();
      closeTab(btn.closest(".tab"));
    });
    strip.addEventListener("auxclick", function (e) {
      if (e.button !== 1) { return; }
      var tab = e.target.closest(".tab");
      if (!tab) { return; }
      e.preventDefault();
      closeTab(tab);
    });
    // Alt+1..9 switches tabs: the browser keeps Ctrl+Tab and Ctrl+W for itself.
    document.addEventListener("keydown", function (e) {
      if (!e.altKey || e.ctrlKey || e.metaKey || e.key < "1" || e.key > "9") { return; }
      var tabs = strip.querySelectorAll(".tab");
      var idx = parseInt(e.key, 10) - 1;
      if (idx < tabs.length) {
        e.preventDefault();
        window.location.assign(tabs[idx].href);
      }
    });
  }

  /* ---- scroll memory --------------------------------------------------- */

  // Switching tabs is a real navigation, so remember where each document was
  // left and put it back when it comes round again.
  function scrollKey() { return "mdl.scroll:" + window.location.pathname; }

  function saveScroll() {
    try { sessionStorage.setItem(scrollKey(), String(window.scrollY)); } catch (e) { /* private mode */ }
  }

  function restoreScroll() {
    try {
      var y = parseInt(sessionStorage.getItem(scrollKey()) || "0", 10);
      if (y > 0) { window.scrollTo(0, y); }
    } catch (e) { /* private mode */ }
  }

  window.addEventListener("pagehide", saveScroll);

  /* ---- live reload --------------------------------------------------- */

  function flashBadge(text) {
    var badge = document.getElementById("reload-badge");
    if (!badge) { return; }
    badge.textContent = text || "updated";
    badge.hidden = false;
    clearTimeout(badge._t);
    badge._t = setTimeout(function () { badge.hidden = true; }, 1800);
  }

  function refreshDocument() {
    fetch(window.location.href, { cache: "no-store", credentials: "same-origin" })
      .then(function (r) { if (!r.ok) { throw new Error("status " + r.status); } return r.text(); })
      .then(function (html) {
        var parsed = new DOMParser().parseFromString(html, "text/html");
        var fresh = parsed.getElementById("doc");
        var current = document.getElementById("doc");
        if (!fresh || !current) { window.location.reload(); return; }

        current.innerHTML = fresh.innerHTML;
        document.title = parsed.title;

        var freshMod = parsed.getElementById("modified");
        var mod = document.getElementById("modified");
        if (freshMod && mod) { mod.textContent = freshMod.textContent; }

        enhance(current);
        buildToc();
        flashBadge("updated");
      })
      .catch(function () { window.location.reload(); });
  }

  function connectEvents() {
    var base = document.body.getAttribute("data-events-url");
    if (!base || !window.EventSource) { return; }
    var url = base + (base.indexOf("?") === -1 ? "?" : "&") + "v=" + encodeURIComponent(VIEWER);
    var es = new EventSource(url, { withCredentials: true });

    es.addEventListener("reload", function () { refreshDocument(); });
    es.addEventListener("gone", function () { flashBadge("file removed"); });
    es.addEventListener("tabs", function () { refreshTabs(); });

    // A file was opened from Explorer and this viewer is the one on screen.
    es.addEventListener("open-doc", function (e) {
      if (!e.data) { return; }
      if (e.data === window.location.pathname) {
        flashBadge("already open");
        refreshTabs();
        return;
      }
      saveScroll();
      window.location.assign(e.data);
    });

    // Too many viewers were holding a connection open, so this one steps back;
    // the browser reconnects by itself once a slot frees up.
    es.addEventListener("busy", function () { flashBadge("live updates paused"); });
  }

  /* ---- installable app ------------------------------------------------ */

  // The worker is not what makes this installable — Chrome dropped that
  // requirement in 112 — it is what a pinned taskbar icon falls back to when
  // the server is not running. It caches nothing else; see web/assets/sw.js.
  if ("serviceWorker" in navigator && window.isSecureContext) {
    window.addEventListener("load", function () {
      navigator.serviceWorker.register("/sw.js", { scope: "/" })
        .catch(function () { /* not installable here; the viewer works anyway */ });
    });
  }

  /* ---- boot ---------------------------------------------------------- */

  function enhance(scope) {
    highlight(scope);
    decorateCodeBlocks(scope);
    renderMermaid(false);
  }

  var doc = document.getElementById("doc");
  if (doc) {
    enhance(doc);
    buildToc();
    // Follow a #fragment that pointed at a heading rendered after load.
    if (window.location.hash) {
      var el = document.getElementById(decodeURIComponent(window.location.hash.slice(1)));
      if (el) { el.scrollIntoView(); }
    } else {
      restoreScroll();
    }
  }
  connectEvents();
})();

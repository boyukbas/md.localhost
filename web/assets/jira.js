/* Jira wiki markup -> HTML.
 *
 * A code block in a Markdown file is verbatim by definition, so nothing here
 * touches the source: the rendered HTML is shown beside the <pre>, and Copy
 * always hands back the original text. The point is to see what a draft will
 * look like once it is pasted into a Jira comment, without leaving the viewer.
 *
 * Faithfulness to Jira is the bar, including its quirks — a single newline is
 * a line break, and -strike- really does fire on a stray hyphen pair. The one
 * deliberate departure is noted at ESCAPABLE below.
 *
 * Every value that reaches the output goes through esc()/attr() first, and the
 * only attributes ever emitted are a validated http(s)/mailto href and a
 * filtered colour, so a draft cannot inject markup into the viewer. */
(function () {
  "use strict";

  function esc(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }
  function attr(s) {
    return esc(s).replace(/"/g, "&quot;");
  }
  // Inline rules run over already-escaped text, so anything lifted back out of
  // it for use as a URL has to be turned back into its real characters first,
  // or "a&b" would reach the href as "a&amp;b".
  function unesc(s) {
    return s.replace(/&gt;/g, ">").replace(/&lt;/g, "<").replace(/&amp;/g, "&");
  }

  /* ---- detection ------------------------------------------------------ */

  // Only constructs that are unambiguously Jira. *bold* and _italic_ are left
  // out on purpose: they turn up in ordinary prose, and offering to "preview"
  // a block that is not markup at all would be worse than doing nothing.
  var STRONG = [
    /^h[1-6]\.\s/m,                                    // h3. Heading
    /^\s*\|\|.+\|\|\s*$/m,                             // ||header||row||
    /\{\{[^{}\n]+\}\}/,                                // {{monospace}}
    /\[~accountid:[^\]\s]+\]/,                         // [~accountid:...]
    /\{code(?::[^}\n]*)?\}/,                           // {code} / {code:sql}
    /\{noformat\}/,
    /\{quote\}/,
    /\{panel(?::[^}\n]*)?\}/,
    /^bq\.\s/m,                                        // bq. quoted line
    /\{color:[^}\n]+\}/,
    /\[[^\]|\n]+\|(?:https?:\/\/|mailto:)[^\]\n]+\]/   // [text|url]
  ];

  function looksLikeJira(text) {
    for (var i = 0; i < STRONG.length; i++) {
      if (STRONG[i].test(text)) { return true; }
    }
    return false;
  }

  /* ---- inline ---------------------------------------------------------- */

  // Fragments that must survive the emphasis pass untouched are parked behind
  // a marker that cannot occur in escaped text.
  function Holder() { this.items = []; }
  Holder.prototype.park = function (html) {
    this.items.push(html);
    return "\u0001" + (this.items.length - 1) + "\u0001";
  };
  Holder.prototype.restore = function (s) {
    var items = this.items;
    return s.replace(/\u0001(\d+)\u0001/g, function (_, i) { return items[+i]; });
  };

  function safeHref(url) {
    return /^(?:https?:\/\/|mailto:)/i.test(url) ? url : null;
  }

  // Takes real text, not escaped text, and does its own escaping.
  function anchor(target, label) {
    return '<a href="' + attr(target) + '" rel="noreferrer noopener" target="_blank">' +
      esc(label) + "</a>";
  }

  // Jira lets a backslash escape any character. Restricting that to characters
  // that are actually markup keeps Windows paths readable in the preview —
  // C:\Users\... would otherwise lose every backslash, which is Jira's real
  // behaviour but never what the author was trying to check.
  var ESCAPABLE = /\\([*_+^~?{}\[\]|\-])/g;

  // marker (regex-escaped), the character itself, tag.
  var EMPHASIS = [
    ["\\*", "*", "strong"],
    ["_", "_", "em"],
    ["\\+", "+", "ins"],
    ["\\^", "^", "sup"],
    ["~", "~", "sub"],
    ["-", "-", "del"]
  ];

  function inline(text, hold) {
    var out = esc(text);

    out = out.replace(/\\\\/g, function () { return hold.park("<br>"); });
    out = out.replace(ESCAPABLE, function (_, ch) { return hold.park(esc(ch)); });

    out = out.replace(/\{\{([^{}\n]+)\}\}/g, function (_, t) {
      return hold.park('<code class="jira-mono">' + t + "</code>");
    });

    // Mentions before subscripts, or [~accountid:...] loses its tildes.
    out = out.replace(/\[~accountid:([^\]\s]+)\]/g, function (_, id) {
      return hold.park('<span class="jira-mention" title="accountid ' + attr(unesc(id)) + '">@mention</span>');
    });
    out = out.replace(/\[~([^\]\s|]+)\]/g, function (_, who) {
      return hold.park('<span class="jira-mention">@' + esc(unesc(who)) + "</span>");
    });

    // [label|url] and [label|url|smart-link]; left as written if the target is
    // not a scheme worth linking.
    out = out.replace(/\[([^\]|\n]+)\|([^\]|\n]+)(?:\|[^\]\n]*)?\]/g, function (whole, label, target) {
      var href = safeHref(unesc(target.trim()));
      return href ? hold.park(anchor(href, unesc(label.trim()))) : whole;
    });
    out = out.replace(/\[((?:https?:\/\/|mailto:)[^\]\s]+)\]/g, function (_, url) {
      var href = unesc(url);
      return hold.park(anchor(href, href));
    });

    out = out.replace(/\{color:([^}\n]+)\}([\s\S]*?)\{color\}/g, function (_, c, body) {
      return '<span style="color:' + attr(c.replace(/[^\w#(),.%\s-]/g, "")) + '">' + body + "</span>";
    });

    out = out.replace(/\?\?(\S(?:[^?\n]*\S)?)\?\?/g, "<cite>$1</cite>");

    EMPHASIS.forEach(function (pair) {
      var mark = pair[0], ch = pair[1], tag = pair[2];
      // Opens only at a word boundary and closes only before whitespace or
      // punctuation, so snake_case and 2026-09-10 are left alone.
      var re = new RegExp(
        "(^|[\\s(\\[{>])" + mark + "(\\S|\\S[^" + ch.replace(/[\^\]\\-]/g, "\\$&") + "\\n]*?\\S)" +
        mark + "(?=$|[\\s)\\]},.;:!?])", "g");
      out = out.replace(re, "$1<" + tag + ">$2</" + tag + ">");
    });

    return out;
  }

  /* ---- blocks ---------------------------------------------------------- */

  function cells(line, header) {
    var body = line.trim();
    if (header) {
      return body.replace(/^\|\|/, "").replace(/\|\|$/, "").split("||");
    }
    return body.replace(/^\|/, "").replace(/\|$/, "").split("|");
  }

  function renderTable(rows, hold) {
    var html = '<table class="jira-table">';
    rows.forEach(function (line) {
      var header = /^\s*\|\|/.test(line);
      var tag = header ? "th" : "td";
      html += "<tr>";
      cells(line, header).forEach(function (c) {
        html += "<" + tag + ">" + inline(c.trim(), hold) + "</" + tag + ">";
      });
      html += "</tr>";
    });
    return html + "</table>";
  }

  // Jira nests by repeating the marker: "** " is a second-level bullet and
  // "*# " a numbered list inside a bulleted one, so the marker's length is the
  // depth and its last character decides the kind.
  function renderList(items, hold) {
    var html = "", stack = [];
    items.forEach(function (it) {
      var depth = it.marker.length;
      var kind = it.marker.charAt(depth - 1) === "#" ? "ol" : "ul";

      while (stack.length > depth) { html += "</li></" + stack.pop() + ">"; }

      if (stack.length === depth) {
        if (stack[depth - 1] === kind) {
          html += "</li><li>";
        } else {
          html += "</li></" + stack.pop() + ">";
        }
      }
      while (stack.length < depth) {
        html += "<" + kind + "><li>";
        stack.push(kind);
      }

      html += inline(it.text, hold);
    });
    while (stack.length) { html += "</li></" + stack.pop() + ">"; }
    return html;
  }

  function paragraph(lines, hold) {
    return "<p>" + lines.map(function (l) { return inline(l, hold); }).join("<br>") + "</p>";
  }

  function verbatim(body, lang) {
    return '<div class="jira-code">' +
      (lang ? '<div class="jira-code-lang">' + esc(lang) + "</div>" : "") +
      "<pre>" + esc(body) + "</pre></div>";
  }

  function render(text) {
    var hold = new Holder();
    var lines = String(text).replace(/\r\n?/g, "\n").split("\n");
    var out = "";
    var para = [];

    function flushPara() {
      if (para.length) { out += paragraph(para, hold); para = []; }
    }

    for (var i = 0; i < lines.length; i++) {
      var line = lines[i];

      // Regions consumed whole, before anything else looks at their contents.
      var open = /^\s*\{(code|noformat|quote|panel)(?::([^}]*))?\}\s*$/.exec(line);
      if (open) {
        flushPara();
        var kind = open[1], params = open[2] || "", body = [];
        var closer = new RegExp("^\\s*\\{" + kind + "\\}\\s*$");
        for (i++; i < lines.length && !closer.test(lines[i]); i++) { body.push(lines[i]); }
        var joined = body.join("\n");
        if (kind === "code" || kind === "noformat") {
          var first = params.split("|")[0] || "";
          var lang = /^(?:lang(?:uage)?=)?([\w+#-]+)$/.exec(first);
          out += verbatim(joined, kind === "code" && lang ? lang[1] : "");
        } else if (kind === "quote") {
          out += "<blockquote>" + render(joined) + "</blockquote>";
        } else {
          var title = /title=([^|]*)/.exec(params);
          out += '<div class="jira-panel">' +
            (title ? '<div class="jira-panel-title">' + esc(title[1]) + "</div>" : "") +
            render(joined) + "</div>";
        }
        continue;
      }

      if (!line.trim()) { flushPara(); continue; }

      var h = /^h([1-6])\.\s*(.*)$/.exec(line);
      if (h) {
        flushPara();
        out += "<h" + h[1] + ">" + inline(h[2], hold) + "</h" + h[1] + ">";
        continue;
      }

      if (/^\s*-{4,}\s*$/.test(line)) { flushPara(); out += "<hr>"; continue; }

      var bq = /^bq\.\s*(.*)$/.exec(line);
      if (bq) {
        flushPara();
        out += "<blockquote><p>" + inline(bq[1], hold) + "</p></blockquote>";
        continue;
      }

      if (/^\s*\|/.test(line)) {
        flushPara();
        var rows = [];
        for (; i < lines.length && /^\s*\|/.test(lines[i]); i++) { rows.push(lines[i]); }
        i--;
        out += renderTable(rows, hold);
        continue;
      }

      // "* " is a bullet; "*bold*" has no space after the marker, so the two
      // cannot be confused. Jira's list markers are * and # only.
      if (/^[*#]+\s+\S/.test(line)) {
        flushPara();
        var items = [];
        for (; i < lines.length; i++) {
          var li = /^([*#]+)\s+(.*)$/.exec(lines[i]);
          if (!li) { break; }
          items.push({ marker: li[1], text: li[2] });
        }
        i--;
        out += renderList(items, hold);
        continue;
      }

      para.push(line);
    }
    flushPara();
    return hold.restore(out);
  }

  window.__mdlJira = { looksLikeJira: looksLikeJira, render: render };
})();

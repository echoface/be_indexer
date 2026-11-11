/* ============================================================
   be_indexer — site interactions
   No dependencies, no build step. Everything degrades gracefully.
   ============================================================ */
(function () {
  "use strict";

  document.documentElement.classList.add("js");

  var reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* ---------- year ---------- */
  document.querySelectorAll("[data-year]").forEach(function (el) {
    el.textContent = String(new Date().getFullYear());
  });

  /* ---------- mobile nav ---------- */
  var navToggle = document.querySelector("[data-nav-toggle]");
  var mainNav = document.querySelector("[data-main-nav]");
  if (navToggle && mainNav) {
    navToggle.addEventListener("click", function () {
      var open = mainNav.classList.toggle("is-open");
      navToggle.setAttribute("aria-expanded", String(open));
    });
    mainNav.querySelectorAll("a").forEach(function (a) {
      a.addEventListener("click", function () {
        mainNav.classList.remove("is-open");
        navToggle.setAttribute("aria-expanded", "false");
      });
    });
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        mainNav.classList.remove("is-open");
        navToggle.setAttribute("aria-expanded", "false");
      }
    });
  }

  /* ---------- header state + progress ---------- */
  var header = document.querySelector(".site-header");
  var progress = document.querySelector("[data-scroll-progress]");
  function onScroll() {
    var y = window.scrollY || window.pageYOffset;
    if (header) header.classList.toggle("is-scrolled", y > 8);
    if (progress) {
      var h = document.documentElement.scrollHeight - window.innerHeight;
      progress.style.width = (h > 0 ? Math.min(100, (y / h) * 100) : 0) + "%";
    }
  }
  window.addEventListener("scroll", onScroll, { passive: true });
  window.addEventListener("resize", onScroll, { passive: true });
  onScroll();

  /* ---------- reveal on enter ---------- */
  var revealEls = document.querySelectorAll(".reveal, .bar-chart");
  if ("IntersectionObserver" in window && !reduceMotion) {
    var revealObs = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          entry.target.classList.add("is-in");
          revealObs.unobserve(entry.target);
        }
      });
    }, { rootMargin: "0px 0px -8% 0px", threshold: 0.08 });
    revealEls.forEach(function (el) { revealObs.observe(el); });
  } else {
    revealEls.forEach(function (el) { el.classList.add("is-in"); });
  }

  /* ---------- copy code ---------- */
  document.querySelectorAll("[data-copy]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var block = btn.closest(".codeblock");
      var code = block ? block.querySelector("pre") : null;
      if (!code) return;
      var text = code.innerText;
      function done() {
        btn.textContent = "已复制";
        btn.classList.add("is-copied");
        window.setTimeout(function () {
          btn.textContent = "复制";
          btn.classList.remove("is-copied");
        }, 1600);
      }
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(done, function () { fallbackCopy(text); done(); });
      } else {
        fallbackCopy(text); done();
      }
    });
  });
  function fallbackCopy(text) {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); } catch (e) {}
    document.body.removeChild(ta);
  }

  /* ---------- lightweight code highlighting ---------- */
  function escapeHTML(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }
  function highlightCode(el) {
    var src = el.textContent;
    var re = /(\/\/[^\n]*|#[^\n]*|"(?:\\.|[^"\\])*"|`(?:\\.|[^`\\])*`|\b(?:func|package|import|return|if|else|for|range|var|const|type|struct|interface|map|chan|go|defer|select|switch|case|default|break|continue|nil|true|false)\b|\b\d[\d_]*(?:\.\d+)?\b)/g;
    var out = "", last = 0, m;
    while ((m = re.exec(src)) !== null) {
      out += escapeHTML(src.slice(last, m.index));
      var t = m[0], cls;
      if (t.slice(0, 2) === "//" || t.charAt(0) === "#") cls = "tok-c";
      else if (t.charAt(0) === '"' || t.charAt(0) === "`") cls = "tok-s";
      else if (/^\d/.test(t)) cls = "tok-n";
      else cls = "tok-k";
      out += '<span class="' + cls + '">' + escapeHTML(t) + "</span>";
      last = m.index + t.length;
    }
    out += escapeHTML(src.slice(last));
    el.innerHTML = out;
  }
  document.querySelectorAll(".codeblock pre code").forEach(highlightCode);

  /* ---------- active TOC ---------- */
  var toc = document.querySelector("[data-toc]");
  if (toc && "IntersectionObserver" in window) {
    var links = Array.prototype.slice.call(toc.querySelectorAll("a"));
    var map = {};
    links.forEach(function (a) {
      var id = a.getAttribute("href").slice(1);
      var section = document.getElementById(id);
      if (section) map[id] = a;
    });
    var current = null;
    var tocObs = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          var a = map[entry.target.id];
          if (a && a !== current) {
            if (current) current.classList.remove("is-active");
            a.classList.add("is-active");
            current = a;
          }
        }
      });
    }, { rootMargin: "-18% 0px -66% 0px", threshold: 0 });
    Object.keys(map).forEach(function (id) {
      var section = document.getElementById(id);
      if (section) tocObs.observe(section);
    });
  }

  /* ============================================================
     Merge visualizer — K-Groups pivot + SkipTo
     ============================================================ */
  var MERGE_FIELDS = [
    { field: "A", values: [10, 20, 40] },
    { field: "B", values: [20, 50] },
    { field: "C", values: [30, 40, 50] }
  ];
  var MERGE_K = 2;

  function cloneMergeState() {
    return {
      cursors: MERGE_FIELDS.map(function (f) {
        return { field: f.field, values: f.values.slice(), pos: 0 };
      }),
      results: [],
      steps: 0,
      last: "",
      lastMatch: null
    };
  }

  function activeCursors(state) {
    return state.cursors.filter(function (c) { return c.pos < c.values.length; });
  }

  function currentVal(c) { return c.values[c.pos]; }

  function computePlan(state) {
    var active = activeCursors(state);
    if (!active.length) {
      return { kind: "done", text: "所有游标耗尽，查询结束。", action: "STOP" };
    }
    var sorted = active.slice().sort(function (a, b) {
      var d = currentVal(a) - currentVal(b);
      return d !== 0 ? d : a.field.localeCompare(b.field);
    });
    var need = Math.max(MERGE_K, 1);
    var pivot = sorted[Math.min(need, sorted.length) - 1];
    var firstK = sorted.slice(0, Math.min(need, sorted.length));
    var pivotVal = currentVal(pivot);
    var aligned = firstK.length === need && firstK.every(function (c) {
      return currentVal(c) === pivotVal;
    });
    if (aligned) {
      return {
        kind: "match",
        pivotVal: pivotVal,
        firstK: firstK,
        pivotFields: firstK.map(function (c) { return c.field; }),
        text: "前 " + need + " 个头部对齐到同一个 Conjunction",
        action: "MATCH " + pivotVal
      };
    }
    return {
      kind: "skip",
      pivotVal: pivotVal,
      firstK: firstK,
      pivotFields: firstK.map(function (c) { return c.field; }),
      text: "pivot 前不足 K 个字段，无法凑齐 " + need + " 个 Include",
      action: "SkipTo(" + pivotVal + ")"
    };
  }

  function applyPlan(state, plan) {
    if (plan.kind === "done") return;
    state.steps += 1;
    if (plan.kind === "match") {
      plan.firstK.forEach(function (c) {
        if (currentVal(c) === plan.pivotVal) c.pos += 1;
      });
      if (state.results.indexOf(plan.pivotVal) === -1) state.results.push(plan.pivotVal);
      state.last = "命中 Conjunction " + plan.pivotVal + " · DocID " + plan.pivotVal + " 进入候选";
      state.lastMatch = plan.pivotVal;
    } else {
      var moved = [];
      plan.firstK.forEach(function (c) {
        var from = currentVal(c);
        while (c.pos < c.values.length && c.values[c.pos] < plan.pivotVal) c.pos += 1;
        if (currentVal(c) !== from) moved.push(c.field + ": " + from + " → " + currentVal(c));
      });
      state.last = "跳过无解区间 · " + (moved.length ? moved.join("，") : "pivot 已对齐");
      state.lastMatch = null;
    }
  }

  function buildMergeDemo(root) {
    var compact = root.hasAttribute("data-compact");
    root.innerHTML =
      '<div class="merge-stage" data-merge-stage></div>' +
      '<div class="merge-status">' +
        '<div class="status-main">' +
          '<div class="status-line"><span class="action" data-merge-action></span> <span data-merge-text></span></div>' +
          '<div class="status-sub" data-merge-last></div>' +
        '</div>' +
        '<div class="step-count" data-merge-step>STEP 00 · K=2</div>' +
      '</div>' +
      '<div class="merge-results" data-merge-results></div>' +
      (compact ? "" :
        '<div class="merge-controls">' +
          '<button class="btn btn-ghost btn-sm" type="button" data-merge-next>单步执行</button>' +
          '<button class="btn btn-ghost btn-sm" type="button" data-merge-play>自动播放</button>' +
          '<button class="btn btn-ghost btn-sm" type="button" data-merge-reset>重置</button>' +
          '<span class="tiny dim mono">示意：EntryID 顺序按 K → index → sign → DocID</span>' +
        '</div>');

    var stage = root.querySelector("[data-merge-stage]");
    var actionEl = root.querySelector("[data-merge-action]");
    var textEl = root.querySelector("[data-merge-text]");
    var lastEl = root.querySelector("[data-merge-last]");
    var stepEl = root.querySelector("[data-merge-step]");
    var resultsEl = root.querySelector("[data-merge-results]");
    var nextBtn = root.querySelector("[data-merge-next]");
    var playBtn = root.querySelector("[data-merge-play]");
    var resetBtn = root.querySelector("[data-merge-reset]");

    var state = cloneMergeState();
    var timer = null;
    var playing = false;
    var visible = true;
    var auto = root.hasAttribute("data-autoplay") && !reduceMotion;

    function render() {
      var plan = computePlan(state);
      var active = activeCursors(state);
      var sorted = active.slice().sort(function (a, b) {
        var d = currentVal(a) - currentVal(b);
        return d !== 0 ? d : a.field.localeCompare(b.field);
      });
      var need = Math.max(MERGE_K, 1);
      var firstK = sorted.slice(0, Math.min(need, sorted.length));
      var pivotFields = firstK.map(function (c) { return c.field; });
      var pivotCol = firstK.length === need && firstK.every(function (c) { return currentVal(c) === currentVal(firstK[firstK.length - 1]); })
        ? firstK.map(function (c) { return c.field; })
        : [firstK[firstK.length - 1] ? firstK[firstK.length - 1].field : ""];

      stage.innerHTML = state.cursors.map(function (c) {
        var items = c.values.map(function (v, i) {
          var cls = "cursor-item";
          var note = "";
          if (i < c.pos) { cls += " is-done"; note = "done"; }
          if (i === c.pos) {
            cls += " is-current";
            note = "head";
            if (plan.kind !== "done" && firstK.indexOf(c) !== -1) cls += " is-head";
            if (plan.kind !== "done" && pivotFields.indexOf(c.field) !== -1 && c.field === (firstK[firstK.length - 1] || {}).field) {
              cls += " is-pivot";
              note = "pivot";
            }
          }
          if (state.lastMatch !== null && i === c.pos - 1 && v === state.lastMatch) {
            cls += " is-match";
            note = "match";
          }
          return '<div class="' + cls + '" data-entry="' + v + '">' +
            '<span class="entry">' + v + '</span><span class="pos">' + note + '</span></div>';
        }).join("");
        var colCls = "cursor-col";
        if (c.pos < c.values.length) colCls += " is-head";
        if (pivotCol.indexOf(c.field) !== -1 && plan.kind !== "done") colCls += " is-pivot";
        return '<div class="' + colCls + '">' +
          '<div class="cursor-title"><b>' + c.field + '</b><span>' + Math.min(c.pos, c.values.length) + '/' + c.values.length + '</span></div>' +
          '<div class="cursor-list">' + items + '</div>' +
          '</div>';
      }).join("");

      actionEl.textContent = plan.kind === "done" ? "STOP" : "下一步 · " + plan.action;
      textEl.textContent = plan.text;
      stepEl.textContent = "STEP " + String(state.steps).padStart(2, "0") + " · K=" + MERGE_K;
      lastEl.textContent = state.last ? "上一步：" + state.last : "初始状态：3 个字段游标按头部 EntryID 竞速。";

      if (state.results.length) {
        resultsEl.innerHTML = '<span class="tiny dim mono">结果 Docs</span>' +
          state.results.map(function (v) { return '<span class="result-doc">' + v + '</span>'; }).join("");
      } else {
        resultsEl.innerHTML = '<span class="result-none">还没有命中的 Document</span>';
      }

      if (nextBtn) nextBtn.disabled = plan.kind === "done";
      if (playBtn) {
        playBtn.textContent = playing ? "暂停" : "自动播放";
        playBtn.disabled = plan.kind === "done" && !playing;
      }
    }

    function step() {
      var plan = computePlan(state);
      if (plan.kind === "done") {
        stop();
        return false;
      }
      applyPlan(state, plan);
      render();
      return computePlan(state).kind !== "done";
    }

    function stop() {
      playing = false;
      if (timer) { window.clearInterval(timer); timer = null; }
      render();
    }

    function reset() {
      stop();
      state = cloneMergeState();
      render();
      if (auto) schedule();
    }

    function schedule() {
      if (timer) window.clearInterval(timer);
      timer = window.setInterval(function () {
        if (!visible) return;
        var hasNext = step();
        if (!hasNext) {
          window.clearInterval(timer);
          timer = null;
          window.setTimeout(function () {
            if (auto && visible) reset();
          }, 1800);
        }
      }, 1350);
    }

    function play() {
      if (computePlan(state).kind === "done") {
        state = cloneMergeState();
        render();
      }
      playing = true;
      schedule();
      render();
    }

    if (nextBtn) nextBtn.addEventListener("click", function () {
      stop();
      step();
    });
    if (resetBtn) resetBtn.addEventListener("click", reset);
    if (playBtn) playBtn.addEventListener("click", function () {
      if (playing) stop(); else play();
    });

    if ("IntersectionObserver" in window) {
      new IntersectionObserver(function (entries) {
        visible = entries[0].isIntersecting;
      }, { threshold: 0.15 }).observe(root);
    }

    render();
    if (auto) {
      window.setTimeout(function () {
        if (visible) play();
      }, 900);
    }
  }
  document.querySelectorAll("[data-merge-demo]").forEach(buildMergeDemo);

  /* ============================================================
     EntryID bitfield explorer
     ============================================================ */
  function buildBitfield() {
    var root = document.querySelector("[data-bitfield]");
    if (!root) return;
    var kEl = root.querySelector("[data-bf-k]");
    var idxEl = root.querySelector("[data-bf-index]");
    var docEl = root.querySelector("[data-bf-doc]");
    var incEl = root.querySelector("[data-bf-include]");
    var grid = root.querySelector("[data-bf-grid]");
    var hexEl = root.querySelector("[data-bf-hex]");
    var decEl = root.querySelector("[data-bf-dec]");
    var conjEl = root.querySelector("[data-bf-conj]");
    if (!kEl || !grid) return;
    var MAX_DOC = 8796093022207; // 2^43 - 1

    function clampInt(v, lo, hi, fallback) {
      v = Number(v);
      if (!isFinite(v)) return fallback;
      return Math.max(lo, Math.min(hi, Math.trunc(v)));
    }

    function bitClass(bit) {
      if (bit >= 56) return "k";
      if (bit >= 48) return "idx";
      if (bit === 47) return "sign";
      if (bit >= 4) return "doc";
      if (bit >= 1) return "res";
      return "inc";
    }

    function render() {
      var k = clampInt(kEl.value, 0, 255, 2);
      var idx = clampInt(idxEl.value, 0, 255, 0);
      var doc = clampInt(docEl.value, -MAX_DOC, MAX_DOC, 20);
      var inc = incEl.checked;
      kEl.value = k; idxEl.value = idx; docEl.value = doc;

      var abs = BigInt(Math.abs(doc));
      var value = (BigInt(k) << 56n) | (BigInt(idx) << 48n);
      if (doc < 0) value |= (1n << 47n);
      value |= (abs << 4n);
      if (inc) value |= 1n;

      var cells = [];
      for (var bit = 63; bit >= 0; bit--) {
        var one = ((value >> BigInt(bit)) & 1n) === 1n;
        cells.push('<span class="bit ' + bitClass(bit) + (one ? " one" : "") +
          '" title="bit ' + bit + '" aria-label="bit ' + bit + ' = ' + (one ? 1 : 0) + '">' +
          (one ? "1" : "0") + "</span>");
      }
      grid.innerHTML = cells.join("");
      hexEl.textContent = "0x" + value.toString(16).toUpperCase().padStart(16, "0");
      decEl.textContent = value.toString();
      conjEl.textContent = "0x" + (value >> 4n).toString(16).toUpperCase().padStart(16, "0");
    }

    [kEl, idxEl, docEl, incEl].forEach(function (el) {
      el.addEventListener("input", render);
      el.addEventListener("change", render);
    });
    root.querySelectorAll("[data-preset]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        kEl.value = btn.getAttribute("data-k") || "0";
        idxEl.value = btn.getAttribute("data-index") || "0";
        docEl.value = btn.getAttribute("data-doc") || "0";
        if (incEl) incEl.checked = btn.getAttribute("data-include") === "1";
        render();
      });
    });
    render();
  }
  buildBitfield();

  /* ============================================================
     Range stabbing demo
     ============================================================ */
  function buildRangeDemo() {
    var root = document.querySelector("[data-range-demo]");
    if (!root) return;
    var track = root.querySelector("[data-range-track]");
    var slider = root.querySelector("[data-range-slider]");
    var readout = root.querySelector("[data-range-readout]");
    var qlabel = root.querySelector("[data-range-q]");
    if (!track || !slider) return;
    var intervals = [
      { id: "D1", lo: 10, hi: 20 },
      { id: "D2", lo: 18, hi: 35 },
      { id: "D3", lo: 30, hi: 60 },
      { id: "D4", lo: 55, hi: 70 },
      { id: "D5", lo: 65, hi: 80 }
    ];
    var bars = intervals.map(function (it, i) {
      var el = document.createElement("div");
      el.className = "range-bar";
      el.textContent = it.id + " [" + it.lo + ", " + it.hi + "]";
      el.style.top = (34 + i * 24) + "px";
      el.style.left = "calc(42px + (100% - 64px) * " + (it.lo / 100) + ")";
      el.style.width = "calc((100% - 64px) * " + ((it.hi - it.lo) / 100) + ")";
      track.appendChild(el);
      return { el: el, it: it };
    });
    var line = document.createElement("div");
    line.className = "range-query";
    line.innerHTML = '<span class="range-query-label" data-range-q-label></span>';
    track.appendChild(line);
    var qlabelEl = line.querySelector("[data-range-q-label]");
    var ticks = document.createElement("div");
    ticks.className = "range-ticks";
    [0, 20, 40, 60, 80, 100].forEach(function (t) {
      var s = document.createElement("span");
      s.style.left = t + "%";
      s.textContent = String(t);
      ticks.appendChild(s);
    });
    track.appendChild(ticks);

    function render(q) {
      line.style.left = "calc(42px + (100% - 64px) * " + (q / 100) + ")";
      if (qlabelEl) qlabelEl.textContent = "q = " + q;
      var hits = [];
      bars.forEach(function (b) {
        var hit = q >= b.it.lo && q <= b.it.hi;
        b.el.classList.toggle("is-hit", hit);
        if (hit) hits.push(b.it.id);
      });
      readout.innerHTML = '<span>stabbing query <strong>q = ' + q + '</strong></span>' +
        '<span>命中区间 <strong>' + (hits.length ? hits.join(" · ") : "∅") + "</strong></span>" +
        '<span class="dim">点查询先走 point index，区间再走 segment tree</span>';
    }
    slider.addEventListener("input", function () { render(Number(slider.value)); });
    render(Number(slider.value || 42));
  }
  buildRangeDemo();

  /* ============================================================
     Aho-Corasick scan demo
     ============================================================ */
  function buildACDemo() {
    var root = document.querySelector("[data-ac-demo]");
    if (!root) return;
    var text = "ushers";
    var steps = [
      { ch: "u", state: "root", id: "0", out: [] },
      { ch: "s", state: "s", id: "2", out: [] },
      { ch: "h", state: "sh", id: "5", out: [] },
      { ch: "e", state: "she", id: "7", out: ["she", "he"] },
      { ch: "r", state: "sher", id: "8", out: [] },
      { ch: "s", state: "shers", id: "9", out: ["hers"] }
    ];
    var charsEl = root.querySelector("[data-ac-chars]");
    var tableEl = root.querySelector("[data-ac-table]");
    var stateEl = root.querySelector("[data-ac-current]");
    var outEl = root.querySelector("[data-ac-output]");
    var svg = root.querySelector("[data-ac-svg]");
    var nextBtn = root.querySelector("[data-ac-next]");
    var resetBtn = root.querySelector("[data-ac-reset]");
    var i = -1;

    charsEl.innerHTML = text.split("").map(function (ch, idx) {
      return '<span class="ac-char" data-idx="' + idx + '">' + ch + "</span>";
    }).join("");
    tableEl.innerHTML = steps.map(function (s, idx) {
      return "<tr data-row=\"" + idx + "\"><td>" + (idx + 1) + "</td><td class=\"mono\">" + s.ch +
        "</td><td class=\"mono\">" + s.id + " · " + s.state + "</td><td>" +
        (s.out.length ? s.out.map(function (o) { return "<code>" + o + "</code>"; }).join(" ") : "—") +
        "</td></tr>";
    }).join("");

    function render() {
      root.querySelectorAll(".ac-char").forEach(function (el, idx) {
        el.classList.toggle("is-active", idx === i);
        el.classList.toggle("is-done", idx < i);
      });
      root.querySelectorAll("[data-row]").forEach(function (el, idx) {
        el.classList.toggle("is-active", idx === i);
      });
      root.querySelectorAll("[data-ac-state]").forEach(function (el) {
        el.classList.toggle("is-active", i >= 0 && el.getAttribute("data-ac-state") === steps[i].state);
      });
      if (i < 0) {
        stateEl.textContent = "等待输入";
        outEl.innerHTML = '<span class="dim">尚未匹配</span>';
        return;
      }
      var s = steps[i];
      stateEl.textContent = s.id + " · " + s.state;
      outEl.innerHTML = s.out.length
        ? s.out.map(function (o) { return '<span class="result-doc">' + o + "</span>"; }).join("")
        : '<span class="dim">无输出</span>';
    }

    if (nextBtn) nextBtn.addEventListener("click", function () {
      if (i < steps.length - 1) { i += 1; render(); }
      else { i = -1; render(); }
    });
    if (resetBtn) resetBtn.addEventListener("click", function () { i = -1; render(); });
    render();
  }
  buildACDemo();

  /* ============================================================
     Benchmark tabs (optional)
     ============================================================ */
  document.querySelectorAll("[data-tabs]").forEach(function (tabs) {
    var buttons = tabs.querySelectorAll("[data-tab]");
    var panels = tabs.querySelectorAll("[data-tab-panel]");
    buttons.forEach(function (btn) {
      btn.addEventListener("click", function () {
        var name = btn.getAttribute("data-tab");
        buttons.forEach(function (b) {
          var on = b === btn;
          b.classList.toggle("is-active", on);
          b.setAttribute("aria-selected", String(on));
        });
        panels.forEach(function (p) {
          p.hidden = p.getAttribute("data-tab-panel") !== name;
        });
      });
    });
  });
})();

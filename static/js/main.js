/* main.js: the site's only script (static/js/main.js, loaded with defer).
   Progressive enhancement throughout: every module looks for its markup and
   returns early, so the same file serves every page and nothing breaks
   without it. No dependencies, no build step. */
(function () {
  'use strict';
  var doc = document, root = doc.documentElement;
  var $ = function (s, el) { return (el || doc).querySelector(s); };
  var $$ = function (s, el) { return Array.prototype.slice.call((el || doc).querySelectorAll(s)); };
  /* Filters compare text in this form: lower case, with the typography the
     site sets (curly quotes, dashes, ellipsis, no-break spaces) folded to
     what a reader types, so "go's" finds "Go’s" and "5-10" finds "5–10". */
  var fold = function (s) {
    return String(s).toLowerCase()
      .replace(/[\u2018\u2019\u201B\u2032]/g, "'").replace(/[\u201C\u201D\u201F\u2033]/g, '"').replace(/[\u2010\u2011\u2013\u2014]/g, '-')
      .replace(/\u2026/g, '...').replace(/[\u00A0\u202F]/g, ' ');
  };

  /* ---- Smooth scrolling from the first settled frame after load (see
     "Smooth in-page scrolling" in style.css): a deep link lands instantly
     and exactly; in-page links and the contents scroll smoothly after. */
  var smooth = function () { requestAnimationFrame(function () { root.classList.add('smooth'); }); };
  if (doc.readyState === 'complete') smooth(); else window.addEventListener('load', smooth);

  /* ---- Announcer: one polite live region for every "Copied" ---------- */
  var live = $('#announcer');
  function announce(msg) {
    if (!live) return;
    live.textContent = '';
    setTimeout(function () { live.textContent = msg; }, 40);
  }

  /* ---- Clipboard: the async API where it is allowed, else execCommand -
     copyText always settles: it resolves once the text is on the
     clipboard and rejects only when both paths failed (a denied
     permission, a non-secure context, an iframe without access). */
  function execCopy(text) {
    var ta = doc.createElement('textarea'), prev = doc.activeElement, ok = false;
    ta.value = text; ta.setAttribute('readonly', '');
    ta.style.cssText = 'position:fixed;top:-999px;opacity:0';
    doc.body.appendChild(ta); ta.select();
    try { ok = doc.execCommand('copy'); } catch (e) {}
    ta.remove();
    /* selecting the textarea took focus: hand it back to the control */
    if (prev && prev !== doc.body && prev.focus) { try { prev.focus({ preventScroll: true }); } catch (e) {} }
    return ok;
  }
  function copyText(text) {
    var fallback = function () { return execCopy(text) ? Promise.resolve() : Promise.reject(new Error('copy failed')); };
    if (navigator.clipboard && window.isSecureContext) {
      try { return navigator.clipboard.writeText(text).catch(fallback); } catch (e) { /* fall through */ }
    }
    return fallback();
  }
  var COPY_FAILED = 'Couldn’t copy';
  /* Flip a control's visible label for 1.6s and announce the result; a
     failed copy says so instead of claiming success. */
  function confirmCopy(btn, msg, failed) {
    var l = $('.copy-label', btn) || btn;
    if (!l.hasAttribute('data-orig')) l.setAttribute('data-orig', l.textContent);
    l.textContent = failed ? COPY_FAILED : 'Copied';
    if (failed) btn.removeAttribute('data-copied'); else btn.setAttribute('data-copied', '');
    announce(failed ? COPY_FAILED : (msg || 'Copied to clipboard'));
    clearTimeout(btn._t);
    btn._t = setTimeout(function () { l.textContent = l.getAttribute('data-orig'); btn.removeAttribute('data-copied'); }, 1600);
  }
  function copyFrom(btn, text, msg) {
    copyText(text).then(function () { confirmCopy(btn, msg); }, function () { confirmCopy(btn, msg, true); });
  }

  /* ---- Theme ----------------------------------------------------------- */
  var themeMeta = $('meta[name="theme-color"]');
  function labelToggles(t) {
    $$('.theme-toggle').forEach(function (b) {
      b.setAttribute('aria-label', t === 'dark' ? 'Switch to light theme' : 'Switch to dark theme');
    });
  }
  var still = window.matchMedia && matchMedia('(prefers-reduced-motion: reduce)');
  function setTheme(t, remember) {
    /* the cross-fade is motion: reduced-motion readers switch at once */
    if (!(still && still.matches)) root.classList.add('theme-anim');
    root.setAttribute('data-theme', t);
    if (remember !== false) { try { localStorage.setItem('theme', t); } catch (e) {} }
    if (themeMeta) themeMeta.setAttribute('content', t === 'dark' ? '#141311' : '#F6F2EA');
    labelToggles(t);
    setTimeout(function () { root.classList.remove('theme-anim'); }, 320);
  }
  function toggleTheme() { setTheme(root.getAttribute('data-theme') === 'dark' ? 'light' : 'dark'); }
  $$('.theme-toggle').forEach(function (b) { b.addEventListener('click', toggleTheme); });
  labelToggles(root.getAttribute('data-theme'));
  /* Until the reader picks a theme, follow the OS when its scheme changes. */
  var scheme = window.matchMedia && matchMedia('(prefers-color-scheme: dark)');
  if (scheme) {
    var followOS = function (e) {
      var t = null;
      try { t = localStorage.getItem('theme'); } catch (err) {}
      if (t !== 'light' && t !== 'dark') setTheme(e.matches ? 'dark' : 'light', false);
    };
    if (scheme.addEventListener) scheme.addEventListener('change', followOS);
    else if (scheme.addListener) scheme.addListener(followOS);
  }

  /* ---- Shortcut hint: ⌘K on Apple platforms, Ctrl K elsewhere --------- */
  /* case-insensitive: Chromium's userAgentData.platform is "macOS" */
  var isMac = /mac|iphone|ipad|ipod/i.test((navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || navigator.userAgent);
  if (!isMac) $$('kbd[data-kbd="mod-k"]').forEach(function (k) { k.textContent = 'Ctrl K'; });
  $$('[data-open-palette]').forEach(function (b) { b.title = 'Search (' + (isMac ? '⌘K' : 'Ctrl K') + ')'; });

  /* ---- Copy buttons ------------------------------------------------------ */
  $$('[data-copy]').forEach(function (b) {
    b.addEventListener('click', function () { copyFrom(b, b.getAttribute('data-copy'), b.getAttribute('data-copy-label')); });
  });
  $$('[data-copy-url]').forEach(function (b) {
    b.addEventListener('click', function () { copyFrom(b, location.origin + location.pathname, 'Link to this article copied'); });
  });

  /* ---- Code listings: line count + copy (server renders the button hidden) */
  $$('.code-block').forEach(function (block, i) {
    var pre = $('pre', block), lang = $('.code-block__lang', block), btn = $('.code-block__copy', block);
    if (!pre) return;
    var text = pre.textContent.replace(/\n$/, '');
    var n = text.split('\n').length;
    if (lang) {
      var s = doc.createElement('span');
      s.className = 'code-block__lines';
      s.textContent = n + (n === 1 ? ' line' : ' lines');
      lang.parentNode.insertBefore(s, lang.nextSibling);
    }
    if (btn) {
      btn.hidden = false;
      btn.setAttribute('aria-label', 'Copy listing ' + (i + 1));
      btn.addEventListener('click', function () { copyFrom(btn, pre.textContent.replace(/\n$/, ''), 'Listing ' + (i + 1) + ' copied'); });
    }
  });

  /* ---- Horizontal scrollers: edge fades only where there is more ------- */
  $$('.tchips').forEach(function (el) {
    function edges() {
      el.toggleAttribute('data-scrolled', el.scrollLeft > 4);
      el.toggleAttribute('data-at-end', el.scrollLeft + el.clientWidth >= el.scrollWidth - 4);
    }
    el.addEventListener('scroll', edges, { passive: true });
    window.addEventListener('resize', edges);
    edges();
    /* Chrome leaves a partly visible chip where it is when it takes focus,
       which can be under a fade: bring it fully in (scroll-padding keeps
       it clear of both fades) */
    el.addEventListener('focusin', function (e) {
      if (e.target !== el && e.target.scrollIntoView) e.target.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    });
  });

  /* ---- Command palette (Cmd/Ctrl+K): native <dialog> + combobox ------- */
  var dlg = $('#palette');
  if (dlg && typeof dlg.showModal === 'function') {
    var input = $('#palette-input', dlg), list = $('#palette-list', dlg);
    var opener = null, visible = [], sel = -1, loaded = false;
    var options = function () { return $$('[role="option"]', list); };
    var select = function (i) {
      sel = i;
      visible.forEach(function (o, j) { o.setAttribute('aria-selected', String(j === i)); });
      var o = visible[i];
      if (o) { input.setAttribute('aria-activedescendant', o.id); o.scrollIntoView({ block: 'nearest' }); }
      else input.removeAttribute('aria-activedescendant');
    };
    var filter = function () {
      var raw = input.value.trim(), q = fold(raw);
      var terms = q.split(/\s+/).filter(Boolean);
      options().forEach(function (o) {
        if (o.hasAttribute('data-search-all')) {
          o.hidden = !raw;
          $('.q', o).textContent = raw;
          o.setAttribute('href', '/search?q=' + encodeURIComponent(raw));
          return;
        }
        var hay = fold(o.textContent + ' ' + (o.getAttribute('data-keywords') || ''));
        o.hidden = !terms.every(function (t) { return hay.indexOf(t) > -1; });
      });
      $$('[role="group"]', list).forEach(function (g) { g.hidden = !$('[role="option"]:not([hidden])', g); });
      visible = options().filter(function (o) { return !o.hidden; });
      select(visible.length ? 0 : -1);
    };
    /* Articles and projects come from /search-index.json on first open;
       the server-rendered items stay if the fetch fails. */
    /* the rows are built as markup, so values land in text AND in quoted
       attributes (href, data-keywords): escape quotes as well as <>& */
    var esc = function (s) {
      return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
        return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
      });
    };
    var month = function (iso) {
      var d = new Date(iso + 'T00:00:00');
      return isNaN(d) ? '' : d.toLocaleDateString('en-GB', { month: 'short', year: 'numeric' });
    };
    var load = function () {
      if (loaded || !list.getAttribute('data-index') || location.protocol === 'file:') return;
      loaded = true;
      fetch(list.getAttribute('data-index'), { credentials: 'same-origin' })
        .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
        .then(function (data) {
          var ga = $('[data-group="articles"]', list), gp = $('[data-group="projects"]', list);
          if (ga && data.posts) {
            ga.innerHTML = ga.firstElementChild.outerHTML + data.posts.map(function (p, i) {
              return '<a id="po-a' + i + '" class="palette-item" role="option" tabindex="-1" href="' + esc(p.url) + '" data-keywords="' + esc((p.tags || []).join(' ')) + '">' + esc(p.title) + ' <span class="meta">' + month(p.date) + '</span></a>';
            }).join('');
          }
          if (gp && data.projects) {
            gp.innerHTML = gp.firstElementChild.outerHTML + data.projects.map(function (p, i) {
              /* the project's short summary (a sentence; CSS ends it in an ellipsis) */
              return '<a id="po-r' + i + '" class="palette-item" role="option" tabindex="-1" href="/projects#' + esc(p.id) + '" data-keywords="' + esc((p.tech || []).join(' ')) + '">' + esc(p.title) + ' <span class="meta meta--clip">' + esc(p.summary || (p.tech || [])[0] || '') + '</span></a>';
            }).join('');
          }
          filter();
        })
        .catch(function () {});
    };
    /* focus returns to the trigger on close; Safari does not focus buttons
       on click, so a clicked trigger is passed in rather than read back */
    /* phones get the short placeholder: the long one runs into Close */
    var narrow = window.matchMedia && matchMedia('(max-width: 599px)'), longHint = input.placeholder;
    var open = function (trigger) {
      opener = trigger || doc.activeElement;
      input.placeholder = (narrow && narrow.matches && input.getAttribute('data-placeholder-short')) || longHint;
      input.value = '';
      filter();
      dlg.showModal();
      input.focus();
      load();
    };
    $$('[data-open-palette]').forEach(function (b) { b.addEventListener('click', function () { open(b); }); });
    window.addEventListener('keydown', function (e) {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        dlg.open ? dlg.close() : open();
      }
    });
    dlg.addEventListener('close', function () { if (opener && opener.focus) opener.focus(); });
    dlg.addEventListener('click', function (e) { if (e.target === dlg) dlg.close(); });
    $$('[data-close-palette]', dlg).forEach(function (b) { b.addEventListener('click', function () { dlg.close(); }); });
    input.addEventListener('input', filter);
    input.addEventListener('keydown', function (e) {
      if (e.key === 'ArrowDown') { e.preventDefault(); select(Math.min(sel + 1, visible.length - 1)); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); select(Math.max(sel - 1, 0)); }
      else if (e.key === 'Home' && e.ctrlKey) { e.preventDefault(); select(0); }
      else if (e.key === 'End' && e.ctrlKey) { e.preventDefault(); select(visible.length - 1); }
      else if (e.key === 'Enter' && visible[sel]) { e.preventDefault(); visible[sel].click(); }
      else if (e.key === 'Tab') { e.preventDefault(); } /* focus stays in the field */
    });
    list.addEventListener('mousemove', function (e) {
      var o = e.target.closest && e.target.closest('[role="option"]');
      var i = visible.indexOf(o);
      if (i > -1 && i !== sel) select(i);
    });
    list.addEventListener('click', function (e) {
      var o = e.target.closest && e.target.closest('[data-action]');
      if (!o) return;
      var a = o.getAttribute('data-action');
      if (a === 'theme') { toggleTheme(); dlg.close(); }
      if (a === 'copy-email') {
        /* the option's meta says so for a beat, then the palette closes */
        var mail = o.getAttribute('data-email'), meta = $('.meta', o); /* from SiteConfig, set by base.html */
        var settle = function (failed) {
          announce(failed ? COPY_FAILED : 'Email address copied');
          if (!meta) { dlg.close(); return; }
          if (!meta.hasAttribute('data-orig')) meta.setAttribute('data-orig', meta.textContent);
          meta.textContent = failed ? COPY_FAILED : 'Copied';
          o.toggleAttribute('data-copied', !failed);
          clearTimeout(o._t);
          o._t = setTimeout(function () {
            if (dlg.open) dlg.close();
            meta.textContent = meta.getAttribute('data-orig');
            o.removeAttribute('data-copied');
          }, failed ? 1600 : 900);
        };
        if (mail) copyText(mail).then(function () { settle(false); }, function () { settle(true); });
      }
    });
  }

  /* ---- Article: permalinks, contents, progress ------------------------- */
  var prose = $('.prose');
  if (prose) {
    var heads = $$(':scope > h2[id], :scope > h3[id]', prose);
    var h2n = 0, h3n = 0;
    var numberOf = {};
    heads.forEach(function (h) {
      if (h.tagName === 'H2') { h2n++; h3n = 0; numberOf[h.id] = (h2n < 10 ? '0' : '') + h2n; }
      else { h3n++; numberOf[h.id] = (h2n < 10 ? '0' : '') + h2n + '.' + h3n; }
      /* the heading text becomes a self-link; clicking also copies it */
      if (!$('.hlink', h)) {
        var a = doc.createElement('a');
        a.className = 'hlink';
        a.href = '#' + h.id;
        while (h.firstChild) a.appendChild(h.firstChild);
        h.appendChild(a);
      }
      $('.hlink', h).addEventListener('click', function () {
        copyText(location.origin + location.pathname + '#' + h.id).then(function () {
          h.setAttribute('data-copied', '');
          announce('Link to section ' + numberOf[h.id] + ' copied');
          clearTimeout(h._t);
          h._t = setTimeout(function () { h.removeAttribute('data-copied'); }, 1600);
        }, function () { announce(COPY_FAILED); });
      });
    });

    var bar = $('.progress'), toc = $('.toc'), pct = $('[data-progress-pct]');
    var now = $('[data-toc-now]'), tocM = $('.toc-m');
    var links = $$('.toc-list a[href^="#"]');
    var byId = {};
    links.forEach(function (a) { var id = a.getAttribute('href').slice(1); (byId[id] = byId[id] || []).push(a); });
    var order = heads.filter(function (h) { return byId[h.id]; });
    var idle = now ? now.textContent : '';
    var last = null, ticking = false;
    var update = function () {
      ticking = false;
      var r = prose.getBoundingClientRect(), vh = window.innerHeight;
      var p = Math.min(1, Math.max(0, -r.top / Math.max(1, r.height - vh * 0.6)));
      if (bar) bar.style.setProperty('--p', p.toFixed(4));
      if (toc) toc.style.setProperty('--p', p.toFixed(4));
      if (pct) pct.textContent = Math.round(p * 100) + '%';
      var cur = -1;
      for (var i = 0; i < order.length; i++) { if (order[i].getBoundingClientRect().top < vh * 0.28) cur = i; }
      var id = cur > -1 ? order[cur].id : null;
      if (id === last) return;
      last = id;
      order.forEach(function (h, j) {
        (byId[h.id] || []).forEach(function (a) {
          if (j === cur) a.setAttribute('aria-current', 'true'); else a.removeAttribute('aria-current');
          a.classList.toggle('is-read', j < cur);
        });
      });
      if (now) now.textContent = id ? '§ ' + numberOf[id] + ' · ' + order[cur].textContent.trim() : idle;
    };
    window.addEventListener('scroll', function () { if (!ticking) { ticking = true; requestAnimationFrame(update); } }, { passive: true });
    window.addEventListener('resize', update);
    update();
    if (tocM) {
      $$('a', tocM).forEach(function (a) { a.addEventListener('click', function () { tocM.open = false; }); });
      /* left open, the stuck panel would cover whatever takes focus next */
      tocM.addEventListener('focusout', function (e) { if (tocM.open && !tocM.contains(e.relatedTarget)) tocM.open = false; });
      tocM.addEventListener('keydown', function (e) {
        if (e.key === 'Escape' && tocM.open) { e.preventDefault(); tocM.open = false; $('summary', tocM).focus(); }
      });
    }
  }

  /* ---- Writing index: topic chips + in-place search -------------------- */
  var entries = $$('.entry[data-tags]');
  var q = $('#q');
  if (entries.length) {
    var chips = $$('.tchip'), groups = $$('[data-year-group]');
    var status = $('#filter-status'), empty = $('[data-empty]'), form = q && q.closest('form');
    var searchAll = $('[data-search-link]');
    var total = entries.length, active = 'all';
    var apply = function () {
      var raw = (q && q.value || '').trim(), term = fold(raw), n = 0;
      entries.forEach(function (e) {
        /* the row's own tag links name its tags exactly; the space-joined
           data-tags cannot hold a tag that contains a space ("web dev") */
        var tags = $$('.tagline a', e).map(function (a) { return a.textContent.trim(); });
        var ok = (active === 'all' || tags.indexOf(active) > -1) && (!term || fold(e.textContent).indexOf(term) > -1);
        e.hidden = !ok; if (ok) n++;
      });
      groups.forEach(function (g) {
        var shown = $$('.entry', g).filter(function (e) { return !e.hidden; }).length;
        g.hidden = shown === 0;
        $$('[data-count]', g).forEach(function (c) { c.textContent = shown + (shown === 1 ? ' article' : ' articles'); });
      });
      if (empty) empty.hidden = n > 0;
      /* the empty state's way out: the full-text search for what was typed */
      if (searchAll) searchAll.setAttribute('href', '/search' + (raw ? '?q=' + encodeURIComponent(raw) : ''));
      if (status) {
        var noun = function (k) { return k === 1 ? ' article' : ' articles'; };
        status.textContent = n === total && !term && active === 'all'
          ? (total === 1 ? 'Showing 1 article' : 'Showing all ' + total + ' articles')
          : 'Showing ' + n + ' of ' + total + noun(total) + (active !== 'all' ? ' tagged ' + active : '') + (term ? ' matching “' + raw + '”' : '');
      }
    };
    /* one topic at a time: pressing the pressed topic again goes back to
       All, so every press of a toggle button changes its state */
    chips.forEach(function (c) {
      c.addEventListener('click', function () {
        var tag = c.getAttribute('data-tag');
        active = tag === active && tag !== 'all' ? 'all' : tag;
        chips.forEach(function (x) { x.setAttribute('aria-pressed', String(x.getAttribute('data-tag') === active)); });
        apply();
      });
    });
    if (q) q.addEventListener('input', apply);
    /* with JS the box filters in place; Enter still runs the full-text search */
    if (form && q) form.addEventListener('submit', function (e) { if (!q.value.trim()) e.preventDefault(); });
  }
})();

/* main.js: the site's only script (static/js/main.js, loaded with defer).
   Every module looks for its markup and returns early, so the same file
   serves every page and the site works without it. No dependencies, no
   build step. The modules, in order:
   - smooth in-page scrolling once the page has loaded;
   - the announcer (one polite live region) and the clipboard helpers;
   - theme: the toggle, following the OS until the reader picks;
   - the search button's shortcut title (⌘K or Ctrl K);
   - Copy buttons: [data-copy] (contact, install) and code blocks;
   - the command palette (⌘K / Ctrl K): a native <dialog> with a combobox;
   - article headings: each h2/h3 becomes a self-link that copies itself. */
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
    /* inside an open modal dialog (the palette), since the page behind it is
       inert: a textarea there could not take focus, and the copy would run
       against the dialog's input and still report success */
    ($('dialog[open]') || doc.body).appendChild(ta); ta.select();
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
    if (!btn.hasAttribute('data-orig')) btn.setAttribute('data-orig', btn.textContent);
    btn.textContent = failed ? COPY_FAILED : 'Copied';
    if (failed) btn.removeAttribute('data-copied'); else btn.setAttribute('data-copied', '');
    announce(failed ? COPY_FAILED : (msg || 'Copied to clipboard'));
    clearTimeout(btn._t);
    btn._t = setTimeout(function () { btn.textContent = btn.getAttribute('data-orig'); btn.removeAttribute('data-copied'); }, 1600);
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
  $$('[data-open-palette]').forEach(function (b) { b.title = 'Search (' + (isMac ? '⌘K' : 'Ctrl K') + ')'; });

  /* ---- Copy buttons ------------------------------------------------------ */
  $$('[data-copy]').forEach(function (b) {
    b.addEventListener('click', function () { copyFrom(b, b.getAttribute('data-copy'), b.getAttribute('data-copy-label')); });
  });

  /* ---- Code blocks: copy (the server renders the button hidden) -------- */
  $$('.code-block').forEach(function (block, i) {
    var pre = $('pre', block), btn = $('.code-block__copy', block);
    if (!pre) return;
    if (btn) {
      btn.hidden = false;
      btn.setAttribute('aria-label', 'Copy code block ' + (i + 1));
      btn.addEventListener('click', function () { copyFrom(btn, pre.textContent.replace(/\n$/, ''), 'Code block ' + (i + 1) + ' copied'); });
    }
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
              return '<a id="po-r' + i + '" class="palette-item" role="option" tabindex="-1" href="/projects#' + esc(p.id) + '" data-keywords="' + esc((p.tech || []).concat(p.summary ? [p.summary] : []).join(' ')) + '">' + esc(p.title) + '</a>';
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

  /* ---- Article: heading self-links ---- */
  var prose = $('.prose');
  if (prose) {
    var heads = $$(':scope > h2[id], :scope > h3[id]', prose);
    heads.forEach(function (h) {
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
          announce('Link to this section copied');
          clearTimeout(h._t);
          h._t = setTimeout(function () { h.removeAttribute('data-copied'); }, 1600);
        }, function () { announce(COPY_FAILED); });
      });
    });
  }
})();

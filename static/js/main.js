(function () {
  'use strict';

  /* ---------- Theme Management ---------- */

  const THEME_KEY = 'theme-preference';

  function getSystemTheme() {
    return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  }

  function applyTheme(theme) {
    var resolved = theme === 'system' ? getSystemTheme() : theme;
    document.documentElement.setAttribute('data-theme', resolved);

    // Update body classes for Tailwind
    var body = document.body;
    if (resolved === 'light') {
      body.classList.remove('bg-[#0A0A0A]', 'text-[#EDEDED]');
      body.classList.add('bg-[#FAFAFA]', 'text-[#171717]');
    } else {
      body.classList.remove('bg-[#FAFAFA]', 'text-[#171717]');
      body.classList.add('bg-[#0A0A0A]', 'text-[#EDEDED]');
    }

    // Update icons
    var sunIcon = document.getElementById('theme-icon-sun');
    var moonIcon = document.getElementById('theme-icon-moon');
    if (sunIcon && moonIcon) {
      if (resolved === 'light') {
        sunIcon.classList.add('hidden');
        moonIcon.classList.remove('hidden');
      } else {
        sunIcon.classList.remove('hidden');
        moonIcon.classList.add('hidden');
      }
    }
  }

  function toggleTheme() {
    var current = localStorage.getItem(THEME_KEY) || 'dark';
    var next = current === 'dark' ? 'light' : 'dark';
    localStorage.setItem(THEME_KEY, next);
    applyTheme(next);
  }

  function initTheme() {
    var saved = localStorage.getItem(THEME_KEY) || 'dark';
    applyTheme(saved);

    var btn = document.getElementById('theme-toggle');
    if (btn) btn.addEventListener('click', toggleTheme);

    var btnMobile = document.getElementById('theme-toggle-mobile');
    if (btnMobile) btnMobile.addEventListener('click', toggleTheme);

    window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', function () {
      if (localStorage.getItem(THEME_KEY) === 'system') applyTheme('system');
    });
  }

  /* ---------- Mobile Navigation ---------- */

  function initMobileNav() {
    var btn = document.getElementById('mobile-menu-btn');
    var menu = document.getElementById('mobile-menu');
    var hamburger = document.getElementById('hamburger-icon');
    var closeIcon = document.getElementById('close-icon');
    if (!btn || !menu) return;

    btn.addEventListener('click', function () {
      var isHidden = menu.classList.toggle('hidden');
      if (hamburger && closeIcon) {
        hamburger.classList.toggle('hidden', !isHidden);
        closeIcon.classList.toggle('hidden', isHidden);
      }
    });
  }

  /* ---------- Smooth Scroll for TOC ---------- */

  function initTocScroll() {
    document.querySelectorAll('.toc-link, a[href^="#"]').forEach(function (link) {
      link.addEventListener('click', function (e) {
        var href = this.getAttribute('href');
        if (!href || href.charAt(0) !== '#') return;

        var target = document.querySelector(href);
        if (!target) return;

        e.preventDefault();
        var top = target.getBoundingClientRect().top + window.pageYOffset - 80;
        window.scrollTo({ top: top, behavior: 'smooth' });
        history.pushState(null, '', href);
      });
    });
  }

  /* ---------- Copy Code Block ---------- */

  function initCopyButtons() {
    document.querySelectorAll('pre').forEach(function (block) {
      if (block.querySelector('.copy-btn')) return;

      var btn = document.createElement('button');
      btn.className = 'copy-btn';
      btn.textContent = 'Copy';
      btn.setAttribute('aria-label', 'Copy code to clipboard');

      btn.addEventListener('click', function () {
        var code = block.querySelector('code');
        var text = code ? code.textContent : block.textContent;

        navigator.clipboard.writeText(text).then(function () {
          btn.textContent = 'Copied!';
          setTimeout(function () { btn.textContent = 'Copy'; }, 2000);
        }).catch(function () {
          btn.textContent = 'Error';
        });
      });

      block.style.position = 'relative';
      block.appendChild(btn);
    });
  }

  /* ---------- Reading Progress Bar ---------- */

  function initReadingProgress() {
    var bar = document.getElementById('reading-progress');
    if (!bar) return;

    // Show the bar on blog post pages
    if (document.querySelector('.prose-content')) {
      bar.classList.remove('hidden');
    }

    var ticking = false;
    window.addEventListener('scroll', function () {
      if (!ticking) {
        window.requestAnimationFrame(function () {
          var scrollTop = window.pageYOffset || document.documentElement.scrollTop;
          var docHeight = document.documentElement.scrollHeight - document.documentElement.clientHeight;
          if (docHeight > 0) {
            bar.style.width = Math.min((scrollTop / docHeight) * 100, 100) + '%';
          }
          ticking = false;
        });
        ticking = true;
      }
    }, { passive: true });
  }

  /* ---------- Init ---------- */

  function init() {
    initTheme();
    initMobileNav();
    initTocScroll();
    initCopyButtons();
    initReadingProgress();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();

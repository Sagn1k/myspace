(function () {
  'use strict';

  /* ---------- Theme Management ---------- */

  var THEME_KEY = 'theme-preference';

  function getSystemTheme() {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }

  function applyTheme(theme) {
    var resolved = theme === 'system' ? getSystemTheme() : theme;
    document.documentElement.setAttribute('data-theme', resolved);

    var sunIcon = document.getElementById('theme-icon-sun');
    var moonIcon = document.getElementById('theme-icon-moon');
    if (sunIcon && moonIcon) {
      if (resolved === 'dark') {
        sunIcon.classList.remove('hidden');
        moonIcon.classList.add('hidden');
      } else {
        sunIcon.classList.add('hidden');
        moonIcon.classList.remove('hidden');
      }
    }
  }

  function toggleTheme() {
    var current = document.documentElement.getAttribute('data-theme') || 'light';
    var next = current === 'dark' ? 'light' : 'dark';
    localStorage.setItem(THEME_KEY, next);
    applyTheme(next);
  }

  function initTheme() {
    var saved = localStorage.getItem(THEME_KEY) || 'light';
    applyTheme(saved);

    var btn = document.getElementById('theme-toggle');
    if (btn) btn.addEventListener('click', toggleTheme);

    var btnMobile = document.getElementById('theme-toggle-mobile');
    if (btnMobile) btnMobile.addEventListener('click', toggleTheme);

    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', function () {
      if (!localStorage.getItem(THEME_KEY)) applyTheme(getSystemTheme());
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
      var isOpen = menu.classList.contains('is-open');
      if (isOpen) {
        menu.classList.remove('is-open');
        setTimeout(function () { menu.style.display = 'none'; }, 250);
      } else {
        menu.style.display = 'block';
        // Force reflow before adding class for animation
        menu.offsetHeight;
        menu.classList.add('is-open');
      }
      if (hamburger && closeIcon) {
        hamburger.classList.toggle('hidden', !isOpen);
        closeIcon.classList.toggle('hidden', isOpen);
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
        var top = target.getBoundingClientRect().top + window.pageYOffset - 72;
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

  /* ---------- Scroll-triggered Animations ---------- */

  function initAnimations() {
    var elements = document.querySelectorAll('.animate-up');
    if (!elements.length) return;

    var observer = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          entry.target.classList.add('is-visible');
          observer.unobserve(entry.target);
        }
      });
    }, { threshold: 0.1, rootMargin: '0px 0px -30px 0px' });

    elements.forEach(function (el) {
      for (var i = 1; i <= 5; i++) {
        if (el.classList.contains('stagger-' + i)) {
          el.style.animationDelay = (i * 0.08) + 's';
          break;
        }
      }
      observer.observe(el);
    });
  }

  /* ---------- Init ---------- */

  function init() {
    initTheme();
    initMobileNav();
    initTocScroll();
    initCopyButtons();
    initReadingProgress();
    initAnimations();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();

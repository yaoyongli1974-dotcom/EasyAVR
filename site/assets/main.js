/* EasyAVR 官网脚本 — 仅移动端导航、代码复制、滚动入场。无第三方依赖。 */
(function () {
  'use strict';

  /* 标记 JS 可用：入场动效只在有 JS 时生效，避免无脚本环境丢内容 */
  document.documentElement.classList.add('js');

  /* ---------- 移动端导航 ---------- */
  var toggle = document.getElementById('nav-toggle');
  var nav = document.getElementById('site-nav');

  function closeNav() {
    if (!nav.classList.contains('open')) return;
    nav.classList.remove('open');
    toggle.setAttribute('aria-expanded', 'false');
  }

  if (toggle && nav) {
    toggle.addEventListener('click', function () {
      var open = nav.classList.toggle('open');
      toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
    });

    nav.addEventListener('click', function (e) {
      if (e.target && e.target.closest('a')) closeNav();
    });

    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') closeNav();
    });

    document.addEventListener('click', function (e) {
      if (!e.target.closest('.topbar')) closeNav();
    });
  }

  /* ---------- 代码块复制 ---------- */
  function fallbackCopy(text) {
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.left = '-9999px';
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); } catch (err) { /* 忽略 */ }
    document.body.removeChild(ta);
  }

  var buttons = document.querySelectorAll('[data-copy]');
  for (var i = 0; i < buttons.length; i++) {
    (function (btn) {
      btn.addEventListener('click', function () {
        var box = btn.closest('.code');
        var code = box ? box.querySelector('pre code') : null;
        if (!code) return;
        var text = code.textContent;
        var restore = function () {
          btn.textContent = '复制';
          btn.classList.remove('done');
        };
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).catch(function () { fallbackCopy(text); });
        } else {
          fallbackCopy(text);
        }
        btn.textContent = '已复制';
        btn.classList.add('done');
        setTimeout(restore, 1600);
      });
    })(buttons[i]);
  }

  /* ---------- 滚动入场 ---------- */
  var reveal = document.querySelectorAll('.reveal');
  var reduced = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  if (!('IntersectionObserver' in window) || reduced || reveal.length === 0) {
    for (var j = 0; j < reveal.length; j++) reveal[j].classList.add('in');
    return;
  }

  var io = new IntersectionObserver(function (entries) {
    entries.forEach(function (entry) {
      if (entry.isIntersecting) {
        entry.target.classList.add('in');
        io.unobserve(entry.target);
      }
    });
  }, { rootMargin: '0px 0px -8% 0px', threshold: 0.08 });

  for (var k = 0; k < reveal.length; k++) io.observe(reveal[k]);
})();

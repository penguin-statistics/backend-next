(function () {
  // Everything here only enhances the page, which works without it: the prompt for agents shows whole, the code
  // selects with a click, an app's section shows by its #anchor (:target).
  var d = document, h = d.documentElement, live = d.getElementById('live');
  var D = h.getAttribute('data-copied');
  var still = matchMedia('(prefers-reduced-motion: reduce)');

  // the prompt for agents: folded to three lines on phones (the style), with a button for the rest
  var wrap = d.querySelector('.agent-w'), more = wrap.querySelector('.more');
  var less = more.textContent, all = more.getAttribute('data-more');
  function fold(on) {
    wrap.classList.toggle('fold', on);
    more.setAttribute('aria-expanded', on ? 'false' : 'true');
    more.textContent = on ? all : less;
  }
  more.hidden = false;
  more.addEventListener('click', function () { fold(!wrap.classList.contains('fold')); });
  fold(true);

  if (navigator.clipboard && window.isSecureContext) {
    [].forEach.call(d.querySelectorAll('button.cp'), function (b) {
      var t = b.querySelector('.t'), label = t.textContent, icon = b.querySelector('use'), timer;
      b.hidden = false;
      b.addEventListener('click', function () {
        var box = b.closest('[data-c]');
        navigator.clipboard.writeText(box.getAttribute('data-c')).then(function () {
          // the button keeps its width while it says Copied
          if (!b.classList.contains('ok')) b.style.minWidth = b.offsetWidth + 'px';
          b.classList.add('ok');
          icon.setAttribute('href', '#i-check');
          t.textContent = D;
          live.textContent = D;
          clearTimeout(timer);
          timer = setTimeout(function () {
            b.classList.remove('ok');
            b.style.minWidth = '';
            icon.setAttribute('href', '#i-copy');
            t.textContent = label;
            live.textContent = '';
          }, 1600);
        }, function () {
          // the clipboard is denied (a policy, an app's web view): select the text, for Ctrl+C
          var n = box.querySelector('.agent-p, pre') || box.querySelector(':scope > code') || box.querySelector(':scope > p') || box;
          if (box.contains(wrap)) fold(false);
          var r = d.createRange(), s = getSelection();
          r.selectNodeContents(n);
          s.removeAllRanges();
          s.addRange(r);
        });
      });
    });
  }

  // the apps: a tab list, the choice kept in the #anchor
  var list = d.querySelector('[data-tabs]'), win = list.closest('.win');
  var tabs = [].slice.call(list.querySelectorAll('a.tab'));
  var panels = tabs.map(function (a) { return d.getElementById(a.getAttribute('href').slice(1)); });
  var until = 'onbeforematch' in d.body;
  list.setAttribute('role', 'tablist');
  [].forEach.call(list.querySelectorAll('ul, li'), function (e) { e.setAttribute('role', 'none'); });
  [].forEach.call(list.querySelectorAll('.grp'), function (e) { e.setAttribute('aria-hidden', 'true'); });
  tabs.forEach(function (a, i) {
    a.setAttribute('role', 'tab');
    a.id = 'tab-' + panels[i].id;
    a.setAttribute('aria-controls', panels[i].id);
    panels[i].setAttribute('role', 'tabpanel');
    panels[i].setAttribute('aria-labelledby', a.id);
    // find in page reaches the other apps (Chrome): it shows the one it found
    panels[i].addEventListener('beforematch', function () { select(i, false, true); });
    a.addEventListener('click', function (e) {
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.button) return;
      e.preventDefault();
      select(i, false, true);
      // the list stays in view while a long section scrolls by: bring the new section's top back
      if (win.getBoundingClientRect().top < 0) win.scrollIntoView({ block: 'start', behavior: still.matches ? 'auto' : 'smooth' });
    });
  });
  function select(i, focus, remember) {
    tabs.forEach(function (a, j) {
      var on = i === j;
      a.setAttribute('aria-selected', on ? 'true' : 'false');
      a.tabIndex = on ? 0 : -1;
      if (on) panels[j].removeAttribute('hidden'); else panels[j].setAttribute('hidden', until ? 'until-found' : '');
    });
    if (focus) tabs[i].focus();
    if (remember) history.replaceState(null, '', '#' + panels[i].id);
  }
  list.addEventListener('keydown', function (e) {
    var i = tabs.indexOf(d.activeElement), n = tabs.length, k = e.key;
    if (i < 0) return;
    if (k === 'ArrowDown' || k === 'ArrowRight') i = (i + 1) % n;
    else if (k === 'ArrowUp' || k === 'ArrowLeft') i = (i + n - 1) % n;
    else if (k === 'Home') i = 0;
    else if (k === 'End') i = n - 1;
    else return;
    e.preventDefault();
    select(i, true, true);
  });
  // a link to an app shows the list above its section (the header is the scroll-padding of the page)
  function margins() {
    var shown = panels.filter(function (p) { return !p.hidden; })[0];
    var top = shown.getBoundingClientRect().top - win.getBoundingClientRect().top;
    panels.forEach(function (p) { p.style.scrollMarginTop = top + 'px'; });
  }
  function fromHash() {
    var id = location.hash.slice(1);
    // a malformed #anchor (%E0%A4%A) names no app
    try { id = decodeURIComponent(id); } catch (e) { id = ''; }
    if (id === 'agent') fold(false);
    for (var i = 0; i < panels.length; i++) if (panels[i].id === id) return i;
    return -1;
  }
  var start = fromHash();
  select(start < 0 ? 0 : start, false, false);
  h.classList.add('tabs');
  margins();
  addEventListener('resize', margins);
  addEventListener('hashchange', function () { var i = fromHash(); if (i >= 0) select(i, false, false); });

  // the section in view, in the header
  var nav = [].slice.call(d.querySelectorAll('.top-nav a')), secs = [].slice.call(d.querySelectorAll('main section.sec')), queued;
  function spy() {
    queued = false;
    var at = null;
    secs.forEach(function (s) { if (s.getBoundingClientRect().top < 120) at = s.id; });
    nav.forEach(function (a) {
      if (a.getAttribute('href') === '#' + at) a.setAttribute('aria-current', 'location'); else a.removeAttribute('aria-current');
    });
  }
  addEventListener('scroll', function () { if (!queued) { queued = true; requestAnimationFrame(spy); } }, { passive: true });
  spy();
})();

/* Progressive enhancement: the server already renders all news and ads. */
(function () {
  'use strict';
  const root = document.getElementById('root');
  if (!root) return;
  document.documentElement.classList.add('js');
  const key = 'tribuna:bookmarks';
  let data = {};
  try {
    data = JSON.parse(document.getElementById('initial-data').textContent);
  } catch (_) {}
  const feedback = root.querySelector('#feedback');
  function notice(message) {
    if (feedback) {
      feedback.textContent = message;
      feedback.hidden = false;
    }
  }
  function getSaved() {
    try {
      const ids = JSON.parse(localStorage.getItem(key) || '[]');
      return Array.isArray(ids) ? ids.filter((x) => typeof x === 'string').slice(0, 200) : [];
    } catch (_) {
      return [];
    }
  }
  function save(id) {
    const ids = getSaved(),
      i = ids.indexOf(id);
    if (i < 0) {
      if (ids.length >= 200) {
        notice('На полке уже 200 материалов. Удалите один, чтобы сохранить новый.');
        return;
      }
      ids.push(id);
    } else ids.splice(i, 1);
    try {
      localStorage.setItem(key, JSON.stringify(ids));
    } catch (_) {
      notice('Браузер запретил локальное хранение. Материал не сохранён.');
    }
  }
  root.querySelectorAll('[data-date]').forEach((el) => {
    if (!el.dataset.date) return;
    const date = new Date(el.dataset.date);
    if (!Number.isNaN(date.getTime()))
      el.textContent = date
        .toLocaleDateString('ru-RU', {
          day: '2-digit',
          month: 'short',
          year: 'numeric',
          timeZone: 'Europe/Moscow',
        })
        .replace(' г.', '');
  });
  function update() {
    const saved = new Set(getSaved());
    root.querySelectorAll('[data-save]').forEach((button) => {
      const active = saved.has(button.dataset.save);
      button.classList.toggle('saved', active);
      button.textContent = active ? 'Сохранено ✓' : 'Сохранить +';
      button.setAttribute('aria-pressed', String(active));
    });
    const list = root.querySelector('[data-bookmarks-list]');
    if (list) {
      let visible = 0;
      list.querySelectorAll('[data-article-id]').forEach((card) => {
        card.hidden = !saved.has(card.dataset.articleId);
        if (!card.hidden) visible++;
      });
      const empty = root.querySelector('[data-bookmarks-empty]');
      if (empty) empty.hidden = visible > 0;
    }
  }
  root.querySelectorAll('[data-save]').forEach((button) =>
    button.addEventListener('click', () => {
      save(button.dataset.save);
      update();
    })
  );
  const cards = Array.from(root.querySelectorAll('[data-article-id]'));
  const byId = new Map((data.feed || []).map((item) => [item.id, item]));
  function filterFeed(category, query, bookmarksOnly) {
    const term = (query || '').trim().toLocaleLowerCase('ru-RU');
    const saved = new Set(getSaved());
    let visible = 0;
    cards.forEach((card) => {
      const item = byId.get(card.dataset.articleId) || {};
      const haystack = `${item.title || ''} ${item.summary || ''}`.toLocaleLowerCase('ru-RU');
      const matches = (!category || category === 'all' || item.tag === category) &&
        (!term || haystack.includes(term)) && (!bookmarksOnly || saved.has(card.dataset.articleId));
      card.hidden = !matches;
      if (matches) visible++;
    });
    const label = bookmarksOnly ? 'сохранённых материалов' : 'материалов';
    notice(`Показано ${visible} ${label}.`);
    return visible;
  }
  function applyLocation() {
    const params = new URLSearchParams(location.search);
    const category = params.get('category') || 'all';
    const query = params.get('q') || '';
    const input = root.querySelector('.mast-search input[name="q"]');
    if (input) input.value = query;
    filterFeed(category, query, location.hash === '#bookmarks');
  }
  const search = root.querySelector('[data-search]');
  if (search) search.addEventListener('submit', (event) => {
    event.preventDefault();
    const input = search.querySelector('input[name="q"]');
    const params = new URLSearchParams(location.search);
    if (input && input.value.trim()) params.set('q', input.value.trim()); else params.delete('q');
    history.pushState({}, '', `${location.pathname}?${params.toString()}#feed`);
    applyLocation();
  });
  root.querySelectorAll('.volume[data-filter]').forEach((link) => link.addEventListener('click', (event) => {
    event.preventDefault();
    const params = new URLSearchParams(location.search);
    const category = link.dataset.filter || 'all';
    if (category === 'all') params.delete('category'); else params.set('category', category);
    history.pushState({}, '', `${location.pathname}?${params.toString()}#feed`);
    root.querySelectorAll('.volume').forEach((item) => item.classList.toggle('active', item === link));
    applyLocation();
  }));
  root.querySelectorAll('a[href*="page=2"]').forEach((link) => link.addEventListener('click', (event) => {
    event.preventDefault();
    notice('В статичном демо доступна первая страница. Полная пагинация работает на сервере.');
  }));
  const bookmarksLink = root.querySelector('a[href$="#bookmarks"]');
  if (bookmarksLink) bookmarksLink.addEventListener('click', (event) => {
    event.preventDefault();
    history.pushState({}, '', `${location.pathname}${location.search}#bookmarks`);
    filterFeed(new URLSearchParams(location.search).get('category') || 'all', new URLSearchParams(location.search).get('q') || '', true);
    document.getElementById('bookmarks')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  });
  window.addEventListener('popstate', applyLocation);
  update();
  window.addEventListener('storage', update);
  root.querySelectorAll('[data-menu-toggle]').forEach((button) =>
    button.addEventListener('click', () => {
      const nav = root.querySelector('[data-menu]');
      if (nav) button.setAttribute('aria-expanded', String(nav.classList.toggle('open')));
    })
  );
  const fresh = root.querySelector('#fresh-news');
  if (fresh && window.EventSource && !data.is_demo) {
    const signature = (data.feed || []).map((item) => item.id).join('|');
    const stream = new EventSource('/api/v1/feed/stream');
    stream.addEventListener('bundle', (event) => {
      try {
        const next = JSON.parse(event.data);
        if (signature !== (next.feed || []).map((item) => item.id).join('|')) fresh.hidden = false;
      } catch (_) {}
    });
    fresh.addEventListener('click', () => location.reload());
    window.addEventListener('pagehide', () => stream.close(), { once: true });
  }
})();

/* Same-origin catalog, shared by the Go site and GitHub Pages. */
(() => {
  'use strict';
  const widget = document.querySelector('[data-bookmakers]');
  if (!widget) return;
  const tabs = Array.from(widget.querySelectorAll('[data-bk-group]'));
  const search = widget.querySelector('[data-bk-search]');
  const scroll = widget.querySelector('[data-bk-scroll]');
  const list = widget.querySelector('[data-bk-list]');
  const status = widget.querySelector('[data-bk-status]');
  const updated = widget.querySelector('[data-bk-updated]');
  const feedback = widget.querySelector('[data-bk-feedback]');
  const retry = widget.querySelector('[data-bk-retry]');
  let catalog;
  let group = 'ru';
  let visible = [];
  const normalize = value => String(value || '').toLocaleLowerCase('ru').replace(/ё/g, 'е').trim();

  function node(tag, className, text) {
    const element = document.createElement(tag);
    if (className) element.className = className;
    if (text !== undefined) element.textContent = text;
    return element;
  }
  function reviewURL(value) {
    try {
      const url = new URL(value);
      return url.protocol === 'https:' && url.hostname === 'bookmaker-ratings.ru' && url.pathname.startsWith('/review/') ? url.href : null;
    } catch { return null; }
  }
  function row(item, index) {
    const article = node('article', 'bk-row');
    article.dataset.bkRow = item.id;
    article.append(node('span', 'bk-rank', String(item.rank || index + 1).padStart(2, '0')));
    const identity = node('div', 'bk-identity');
    const mark = node('span', 'bk-mark', item.name.replace(/[^\p{L}\p{N}]/gu, '').slice(0, 2).toLocaleUpperCase('ru'));
    mark.setAttribute('aria-hidden', 'true');
    mark.dataset.tone = String(index % 4);
    const name = node('div', 'bk-name');
    name.append(node('h3', '', item.name));
    name.append(node('span', '', group === 'ru' ? 'Россия' : 'Выбор источника'));
    identity.append(mark, name);
    article.append(identity);
    const offer = node('div', 'bk-offer');
    const best = group === 'best';
    offer.append(node('small', '', best ? 'Мин. депозит' : 'Предложение'), node('strong', '', best ? item.minDeposit || '—' : item.bonus || '—'));
    article.append(offer);
    const promo = node('div', 'bk-promo');
    promo.append(node('small', '', best ? 'Маржа' : 'Промокод'));
    if (best) {
      promo.append(node('span', 'bk-review-count', item.margin || '—'));
    } else if (item.promo) {
      const copy = node('button', 'bk-copy', item.promo);
      copy.type = 'button';
      copy.setAttribute('aria-label', `Скопировать промокод ${item.promo} для ${item.name}`);
      copy.title = 'Скопировать промокод';
      copy.addEventListener('click', async () => {
        try {
          await navigator.clipboard.writeText(item.promo);
          feedback.textContent = `Промокод ${item.promo} скопирован`;
        } catch { feedback.textContent = `Промокод: ${item.promo}`; }
      });
      promo.append(copy);
    } else promo.append(node('span', 'bk-missing', '—'));
    article.append(promo);
    const url = reviewURL(item.reviewUrl);
    if (url) {
      const link = node('a', 'bk-review', 'Обзор ↗');
      link.href = url;
      link.target = '_blank';
      link.rel = 'noopener noreferrer';
      link.setAttribute('aria-label', `Обзор ${item.name} на сайте источника`);
      article.append(link);
    }
    return article;
  }
  function updateStatus() {
    if (!catalog) return;
    const total = catalog.groups[group].items.length;
    const rows = Array.from(list.children).filter(el => el.hasAttribute('data-bk-row'));
    const viewport = scroll.getBoundingClientRect();
    const onScreen = rows.map((el, i) => ({ i, box: el.getBoundingClientRect() }))
      .filter(({ box }) => box.bottom > viewport.top + 42 && box.top < viewport.bottom);
    const range = onScreen.length ? `${onScreen[0].i + 1}–${onScreen.at(-1).i + 1}` : '0';
    const message = search.value.trim()
      ? `Найдено: ${visible.length} из ${total}`
      : `${range} из ${total} · ${catalog.groups[group].label}`;
    if (status.textContent !== message) status.textContent = message;
    widget.querySelector('[data-bk-up]').disabled = scroll.scrollTop < 2;
    widget.querySelector('[data-bk-down]').disabled = scroll.scrollTop + scroll.clientHeight >= scroll.scrollHeight - 2;
  }
  function render() {
    if (!catalog) return;
    const source = catalog.groups[group].sourceUrl;
    const sourceLink = widget.querySelector('[data-bk-source]');
    if (sourceLink && source) {
      try { const u = new URL(source); if (u.protocol === 'https:' && u.hostname === 'bookmaker-ratings.ru') sourceLink.href = u.href; } catch {}
    }
    const query = normalize(search.value);
    visible = catalog.groups[group].items.filter(item => normalize(item.name).includes(query));
    const fragment = document.createDocumentFragment();
    visible.forEach((item, index) => fragment.append(row(item, index)));
    if (!visible.length) fragment.append(node('p', 'bk-empty', 'Не нашли такого букмекера. Измените запрос или переключите список.'));
    list.replaceChildren(fragment);
    scroll.scrollTop = 0;
    scroll.setAttribute('aria-labelledby', `bk-tab-${group}`);
    widget.querySelector('[data-bk-offer-heading]').textContent = group === 'best' ? 'Мин. депозит' : 'Предложение';
    widget.querySelector('[data-bk-promo-heading]').textContent = group === 'best' ? 'Маржа' : 'Промокод';
    tabs.forEach(tab => {
      const active = tab.dataset.bkGroup === group;
      tab.setAttribute('aria-selected', String(active));
      tab.tabIndex = active ? 0 : -1;
    });
    requestAnimationFrame(updateStatus);
  }
  tabs.forEach((tab, index) => {
    tab.addEventListener('click', () => {
      group = tab.dataset.bkGroup;
      render();
    });
    tab.addEventListener('keydown', event => {
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
      event.preventDefault();
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
      tabs[next].focus();
      tabs[next].click();
    });
  });
  search.addEventListener('input', render);
  search.addEventListener('keydown', event => {
    if (event.key === 'Escape') { search.value = ''; render(); }
  });
  scroll.addEventListener('scroll', updateStatus, { passive: true });
  window.addEventListener('resize', updateStatus);
  for (const [selector, direction] of [['[data-bk-up]', -1], ['[data-bk-down]', 1]]) {
    widget.querySelector(selector).addEventListener('click', () => {
      scroll.scrollBy({ top: direction * Math.max(160, scroll.clientHeight - 90), behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth' });
    });
  }
  async function load() {
    retry.hidden = true;
    updated.textContent = 'Загружаем каталог…';
    scroll.setAttribute('aria-busy', 'true');
    try {
      const response = await fetch(widget.dataset.source, { cache: 'no-cache' });
      if (!response.ok) throw new Error('Catalog unavailable');
      const next = await response.json();
      for (const id of tabs.map(tab => tab.dataset.bkGroup)) {
        if (!Array.isArray(next.groups?.[id]?.items) || !next.groups[id].items.length) throw new Error('Catalog incomplete');
      }
      catalog = next;
      tabs.forEach(tab => tab.querySelector('[data-bk-count]').textContent = String(catalog.groups[tab.dataset.bkGroup].items.length));
      const date = new Date(catalog.fetchedAt);
      updated.textContent = Number.isNaN(date.getTime()) ? 'Данные источника' : `Обновлено ${date.toLocaleDateString('ru-RU', { day: '2-digit', month: 'short', year: 'numeric' })}`;
      render();
    } catch {
      updated.textContent = 'Каталог временно недоступен';
      status.textContent = 'Не удалось загрузить список';
      list.replaceChildren(node('p', 'bk-empty', 'Не удалось загрузить каталог. Попробуйте ещё раз или откройте источник ниже.'));
      retry.hidden = false;
    } finally { scroll.setAttribute('aria-busy', 'false'); }
  }
  retry.addEventListener('click', load);
  load();
})();

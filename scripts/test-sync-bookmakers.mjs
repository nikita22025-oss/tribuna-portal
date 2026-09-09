#!/usr/bin/env node

import assert from 'node:assert/strict';
import { parseCatalog } from './sync-bookmakers.mjs';

// Small source-shaped fixture: one RU card with bonus/promo and one
// best-list card with deposit/margin. The live source is
// intentionally not fetched by this test.
const fixture = `
<h2>Все букмекерские конторы РФ</h2>
<table class="bk-table"><tbody><tr><td class="bk-base-logo"><div>1</div><div class="bk-logo-wr"><img alt="Тест БК"></div></td>
<td class="bk-base-cell"><div class="bk-base-cell-value">3</div></td>
<td class="bk-base-cell"><div class="bk-base-cell-value">10 000 ₽</div></td>
<td class="bk-base-cell"><div>TEST10</div><div>Промокод</div></td>
<td><a href="https://bookmaker-ratings.ru/review/test-ru/" data-testid="bk-card-review-button">Обзор</a></td></tr></tbody></table>
<h2>Все нелегальные букмекерские конторы мира</h2>
<h2>Лучшие БК ⬇</h2>
<tr class="mb-2 bk-table"><td><img alt="Best Test"></td>
<td><div class="bk-base-cell-value">100 ₽</div><div class="bk-base-cell-title">Мин депозит</div></td>
<td><div class="bk-base-cell-value">6,5%</div><div class="bk-base-cell-title">Маржа</div></td>
<td><a href="https://bookmaker-ratings.ru/review/test-best/" data-testid="bk-card-review-button">Обзор</a></td></tr>
<h2>Лучшие бонусы</h2>`;

const snapshot = parseCatalog(fixture, '2026-01-01T00:00:00.000Z');
assert.equal(snapshot.groups.ru.total, 1);
assert.equal(snapshot.groups.ru.items[0].bonus, '10 000 ₽');
assert.equal(snapshot.groups.ru.items[0].promo, 'TEST10');
assert.equal(snapshot.groups.best.total, 1);
assert.equal(snapshot.groups.best.items[0].minDeposit, '100 ₽');
assert.equal(snapshot.groups.best.items[0].margin, '6,5%');
assert.equal(snapshot.groups.best.items[0].rank, 1);
assert.equal(snapshot.groups.best.items[0].reviewCount, undefined);
console.log('sync-bookmakers fixture: ok');

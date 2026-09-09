#!/usr/bin/env node

/** Fetch the complete Russian directory and the user's selected best-bookmaker list.
 * Run: node scripts/sync-bookmakers.mjs
 * Fixtures: --input <Russian HTML> --best-input <best-list HTML>
 */

import { readFile, rename, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const SOURCE_URL = 'https://bookmaker-ratings.ru/bookmakers-homepage/vse-bukmekerskie-kontory/';
const BEST_SOURCE_URL = 'https://bookmaker-ratings.ru/bookmakers-homepage/luchshie-bukmekerskie-kontory/';
const ROOT = resolve(new URL('..', import.meta.url).pathname);
const OUTPUTS = [resolve(ROOT, 'frontend/bookmakers.json'), resolve(ROOT, 'docs/bookmakers.json')];

function decodeEntities(value) {
  return value
    .replace(/&#(x[0-9a-f]+|[0-9]+);/gi, (_, code) => {
      const n = code[0].toLowerCase() === 'x' ? parseInt(code.slice(1), 16) : parseInt(code, 10);
      return Number.isFinite(n) ? String.fromCodePoint(n) : _;
    })
    .replace(/&nbsp;/gi, ' ')
    .replace(/&amp;/gi, '&')
    .replace(/&quot;/gi, '"')
    .replace(/&#39;|&apos;/gi, "'")
    .replace(/&lt;/gi, '<')
    .replace(/&gt;/gi, '>');
}

function textFromHtml(value) {
  return decodeEntities(value
    .replace(/<br\s*\/?>/gi, ' ')
    .replace(/<[^>]*>/g, ' ')
    .replace(/\s+/g, ' ')
    .trim());
}

function absoluteUrl(value) {
  try {
    const url = new URL(decodeEntities(value), SOURCE_URL);
    if (url.protocol !== 'https:' || url.hostname !== 'bookmaker-ratings.ru' || !url.pathname.startsWith('/review/')) return '';
    return url.href;
  } catch { return ''; }
}

function catalogueBlocks(html) {
  return [...html.matchAll(/<(table|tr)\b[^>]*class=["'][^"']*\bbk-table\b[^"']*["'][^>]*>[\s\S]*?<\/\1>/gi)]
    .map((match) => match[0]);
}

function sectionBetween(html, heading, nextHeading) {
  const start = html.indexOf(heading);
  if (start < 0) throw new Error(`Source heading not found: ${heading}`);
  const end = nextHeading ? html.indexOf(nextHeading, start + heading.length) : html.length;
  return html.slice(start, end < 0 ? html.length : end);
}

function parseItems(section, group) {
  const blocks = catalogueBlocks(section);
  if (!blocks.length) throw new Error(`No bookmaker cards found in ${group} section`);
  return blocks.map((block, index) => {
    const img = block.match(/<img\b[^>]*\balt=["']([^"']+)["']/i);
    const hiddenName = block.match(/<span\b[^>]*class=["'][^"']*hidden[^"']*["'][^>]*>([\s\S]*?)<\/span>/i);
    const name = decodeEntities(img?.[1] || textFromHtml(hiddenName?.[1] || ''));
    const rankMatch = block.match(/<td\b[^>]*\bbk-base-logo\b[^>]*>[\s\S]*?<div\b[^>]*>(\d+)<\/div>/i);
    const reviewMatch = block.match(/<a\b[^>]*href=["']([^"']+)["'][^>]*data-testid=["']bk-card-review-button["']/i);
    if (!name || !reviewMatch) throw new Error(`Incomplete ${group} card at position ${index + 1}`);

    const item = {
      id: `${group}-${index + 1}`,
      name,
      // International cards have no ordinal in the source markup; their
      // published order is the ranking. Never mistake a logo/metric number
      // deeper in the card for an ordinal.
      rank: Number(rankMatch?.[1] || index + 1),
      reviewUrl: absoluteUrl(reviewMatch[1]),
    };

    // The Russian list exposes bonus and promo cells. We retain their exact
    // published values and omit empty placeholders such as “Нет”.
    if (group === 'ru') {
      const values = [...block.matchAll(/<div\b[^>]*class=["'][^"']*\bbk-base-cell-value\b[^"']*["'][^>]*>([\s\S]*?)<\/div>/gi)]
        .map((m) => textFromHtml(m[1]));
      const bonus = values[1] && !/^(нет|—|-)$/i.test(values[1]) ? values[1] : '';
      if (bonus) item.bonus = bonus;

      const promoCells = [...block.matchAll(/<td\b[^>]*class=["'][^"']*\bbk-base-cell\b[^"']*["'][^>]*>([\s\S]*?)<\/td>/gi)];
      const promoCell = promoCells.map((m) => textFromHtml(m[1])).find((text) => /Промокод/i.test(text));
      if (promoCell) {
        const promo = promoCell.replace(/\s*Промокод[\s\S]*$/i, '').trim();
        if (promo && !/^(не нужен|нет|—|-)$/i.test(promo)) item.promo = promo;
      }
    } else {
      const cells = [...block.matchAll(/<div\b[^>]*class=["'][^"']*\bbk-base-cell-value\b[^"']*["'][^>]*>([\s\S]*?)<\/div>\s*<div\b[^>]*class=["'][^"']*\bbk-base-cell-title\b[^"']*["'][^>]*>([\s\S]*?)<\/div>/gi)];
      for (const cell of cells) {
        const label = textFromHtml(cell[2]);
        const value = textFromHtml(cell[1]);
        if (/^Мин депозит$/i.test(label)) item.minDeposit = value;
        if (/^Маржа$/i.test(label)) item.margin = value;
      }
    }
    return item;
  });
}

function validateCatalog(catalog) {
  for (const [groupId, group] of Object.entries(catalog.groups)) {
    if (!Array.isArray(group.items) || !group.items.length || group.total !== group.items.length) {
      throw new Error(`Invalid ${groupId} item count`);
    }
    const ids = new Set();
    const reviews = new Set();
    const ranks = new Set();
    for (const item of group.items) {
      if (ids.has(item.id)) throw new Error(`Duplicate ${groupId} id at ${item.name}`);
      if (reviews.has(item.reviewUrl)) throw new Error(`Duplicate ${groupId} reviewUrl at ${item.name}: ${item.reviewUrl}`);
      if (ranks.has(item.rank)) throw new Error(`Duplicate ${groupId} rank at ${item.name}: ${item.rank}`);
      ids.add(item.id); reviews.add(item.reviewUrl); ranks.add(item.rank);
      if (!item.name || !Number.isInteger(item.rank) || item.rank < 1 || !absoluteUrl(item.reviewUrl)) {
        throw new Error(`Invalid ${groupId} card at ${item.name || item.id}`);
      }
    }
  }
  return catalog;
}

export function parseCatalog(html, fetchedAt = new Date().toISOString(), bestHtml = html) {
  const ruSection = sectionBetween(html, 'Все букмекерские конторы РФ', 'Все нелегальные букмекерские конторы мира');
  const bestSection = sectionBetween(bestHtml, 'Лучшие БК ⬇', 'Лучшие бонусы');
  const bestBlocks = catalogueBlocks(bestSection);
  if (!bestBlocks.length) throw new Error('No cards found in selected best list');
  const groups = {
    ru: {
      label: 'Россия',
      section: 'Все букмекерские конторы РФ',
      total: 0,
      items: parseItems(ruSection, 'ru'),
    },
    best: {
      label: 'Лучшие',
      section: 'Топ букмекерских контор для ставок на спорт 2026',
      sourceUrl: BEST_SOURCE_URL,
      total: 0,
      items: parseItems(bestBlocks.join(''), 'best'),
    },
  };
  groups.ru.sourceUrl = SOURCE_URL;
  groups.ru.total = groups.ru.items.length;
  groups.best.total = groups.best.items.length;
  return validateCatalog({ sourceUrl: SOURCE_URL, fetchedAt, groups });
}

async function writeAtomic(path, contents) {
  const temp = `${path}.${process.pid}.tmp`;
  await writeFile(temp, contents, 'utf8');
  await rename(temp, path);
}

async function guardAgainstTruncation(snapshot) {
  try {
    const previous = JSON.parse(await readFile(OUTPUTS[0], 'utf8'));
    for (const id of ['ru', 'best']) {
      const before = Number(previous.groups?.[id]?.total || 0);
      const after = Number(snapshot.groups?.[id]?.total || 0);
      if (before > 0 && after < before * 0.8) {
        throw new Error(`Refusing ${id} overwrite: ${after} items is below 80% of previous ${before}`);
      }
    }
  } catch (error) {
    if (error.code === 'ENOENT') return;
    throw error;
  }
}

async function loadSource(url = SOURCE_URL) {
  const inputIndex = process.argv.indexOf(url === BEST_SOURCE_URL ? '--best-input' : '--input');
  if (inputIndex >= 0 && process.argv[inputIndex + 1]) return readFile(resolve(process.argv[inputIndex + 1]), 'utf8');
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 30_000);
  try {
    const response = await fetch(url, { headers: { 'user-agent': 'Tribuna bookmaker catalogue sync/1.0' }, signal: controller.signal });
    if (!response.ok) throw new Error(`Source returned HTTP ${response.status}`);
    return await response.text();
  } finally {
    clearTimeout(timeout);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const html = await loadSource(SOURCE_URL);
  const bestHtml = await loadSource(BEST_SOURCE_URL);
  const snapshot = parseCatalog(html, new Date().toISOString(), bestHtml);
  await guardAgainstTruncation(snapshot);
  const serialized = `${JSON.stringify(snapshot, null, 2)}\n`;
  for (const output of OUTPUTS) await writeAtomic(output, serialized);
  console.log(`Saved ${snapshot.groups.ru.total} Russian + ${snapshot.groups.best.total} best bookmakers`);
  console.log(`Outputs: ${OUTPUTS.join(', ')}`);
}

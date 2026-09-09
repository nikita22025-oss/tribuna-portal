#!/usr/bin/env node

/**
 * Acceptance test for the native bookmaker widget.
 *
 * Usage:
 *   WIDGET_TEST_URL=http://127.0.0.1:8977 \
 *     node scripts/test-bookmakers-widget.mjs
 *
 * The page is expected to expose the bookmaker JSON URL in the widget's
 * `data-source` attribute.  Keeping the catalog as the source of truth means
 * this test checks every published row, rather than a hard-coded sample.
 */

import assert from "node:assert/strict";
import process from "node:process";
import { chromium } from "playwright";

const baseURL = process.env.WIDGET_TEST_URL || "http://127.0.0.1:8977";
const timeout = Number(process.env.WIDGET_TEST_TIMEOUT || 15_000);
const widgetSelector = "[data-bookmakers]";
const rowSelector = "[data-bk-row]";

const normalize = (value) => String(value ?? "").replace(/\s+/g, " ").trim().toLocaleLowerCase();

function itemLabel(item) {
  if (typeof item === "string") return item;
  if (!item || typeof item !== "object") return "";
  for (const key of ["name", "title", "label", "bookmaker", "brand", "operator", "displayName", "slug"]) {
    if (item[key]) return String(item[key]);
  }
  return "";
}

function groupItems(catalog, key) {
  const group = catalog?.groups?.[key];
  const items = Array.isArray(group) ? group : group?.items;
  assert.ok(Array.isArray(items), `data-source groups.${key}.items must be an array`);
  assert.ok(items.length > 0, `data-source groups.${key}.items must not be empty`);
  return items;
}

async function readCatalog(page) {
  const rawSource = await page.locator(widgetSelector).getAttribute("data-source");
  assert.ok(rawSource, `${widgetSelector} must expose a non-empty data-source attribute`);

  // Inline JSON is convenient for local fixtures; production uses a URL.
  if (/^\s*[\[{]/.test(rawSource)) return JSON.parse(rawSource);

  const sourceURL = new URL(rawSource, page.url()).toString();
  const response = await fetch(sourceURL);
  assert.ok(response.ok, `Could not fetch bookmaker catalog (${response.status} ${sourceURL})`);
  return response.json();
}

async function waitForWidget(page) {
  await page.waitForSelector(widgetSelector, { state: "visible", timeout });
  const scroll = page.locator("[data-bk-scroll]");
  await scroll.waitFor({ state: "visible", timeout });
  await page.waitForFunction(
    () => document.querySelector("[data-bk-scroll]")?.getAttribute("aria-busy") === "false",
    undefined,
    { timeout },
  );
  const updated = page.locator("[data-bk-updated]");
  await updated.waitFor({ state: "visible", timeout });
  const statusText = (await updated.textContent())?.trim() || "";
  assert.ok(statusText, "[data-bk-updated] should contain a load/update status");
  assert.doesNotMatch(statusText, /загруз|loading|ошиб|error|недоступ/i, "bookmaker data did not finish loading");
  return statusText;
}

function visibleRows(page) {
  return page.locator(`${rowSelector}:visible`);
}

async function activateGroup(page, key) {
  const button = page.locator(`[data-bk-group="${key}"]`);
  assert.equal(await button.count(), 1, `missing bookmaker group button: ${key}`);
  assert.equal(await button.evaluate((el) => el.tagName), "BUTTON", `${key} control must be a button`);
  assert.equal(await button.getAttribute("role"), "tab", `${key} control must expose role=tab`);
  await button.click();
  await page.waitForTimeout(50);
  assert.equal(await button.getAttribute("aria-selected"), "true", `${key} tab did not become selected`);
  return button;
}

async function assertRowsMatchGroup(page, items, key) {
  const rows = visibleRows(page);
  assert.equal(await rows.count(), items.length, `${key}: rendered row count differs from data-source`);
  const firstExpected = normalize(itemLabel(items[0]));
  const lastExpected = normalize(itemLabel(items.at(-1)));
  assert.ok(firstExpected, `${key}: first catalog item has no display name`);
  assert.ok(lastExpected, `${key}: last catalog item has no display name`);
  const firstText = normalize(await rows.first().innerText());
  const lastText = normalize(await rows.last().innerText());
  assert.ok(firstText.includes(firstExpected), `${key}: first row does not match catalog (${firstExpected})`);
  assert.ok(lastText.includes(lastExpected), `${key}: last row does not match catalog (${lastExpected})`);
  return rows;
}

async function assertRowInRegion(row, region, message) {
  const visible = await pageRectsOverlap(row, region);
  assert.ok(visible, message);
}

async function pageRectsOverlap(row, region) {
  const [rowBox, regionBox] = await Promise.all([row.boundingBox(), region.boundingBox()]);
  if (!rowBox || !regionBox) return false;
  const rowBottom = rowBox.y + rowBox.height;
  const regionBottom = regionBox.y + regionBox.height;
  return rowBottom > regionBox.y && rowBox.y < regionBottom;
}

async function exerciseScroll(page, items, key) {
  const region = page.locator("[data-bk-scroll]");
  assert.equal(await region.count(), 1, "missing [data-bk-scroll] region");
  const rows = visibleRows(page);
  const dimensions = await region.evaluate((el) => ({
    clientHeight: el.clientHeight,
    scrollHeight: el.scrollHeight,
    clientWidth: el.clientWidth,
    scrollWidth: el.scrollWidth,
  }));
  assert.ok(dimensions.scrollWidth <= dimensions.clientWidth + 1, `${key}: horizontal overflow in bookmaker list`);
  if (items.length > 3) {
    assert.ok(dimensions.scrollHeight > dimensions.clientHeight, `${key}: long list is not vertically scrollable`);
  }

  await region.evaluate((el) => el.scrollTo({ top: 0, behavior: "instant" }));
  await page.waitForTimeout(25);
  await assertRowInRegion(rows.first(), region, `${key}: first row is not reachable at scroll top`);

  await region.evaluate((el) => el.scrollTo({ top: el.scrollHeight, behavior: "instant" }));
  await page.waitForTimeout(25);
  await assertRowInRegion(rows.last(), region, `${key}: last row is not reachable at scroll bottom`);
  const atBottom = await region.evaluate((el) => el.scrollTop + el.clientHeight >= el.scrollHeight - 2);
  assert.ok(atBottom || dimensions.scrollHeight <= dimensions.clientHeight, `${key}: scroll did not reach the end`);
}

async function exerciseSearch(page, items, key) {
  const input = page.locator("[data-bk-search]");
  assert.equal(await input.count(), 1, "missing [data-bk-search] input");
  const query = itemLabel(items[0]);
  assert.ok(query, `${key}: cannot exercise search without a catalog item name`);
  await input.fill(query);
  await page.waitForTimeout(50);
  const matchingRows = visibleRows(page);
  assert.ok(await matchingRows.count() >= 1, `${key}: search returned no matching row`);
  assert.ok(normalize(await matchingRows.first().innerText()).includes(normalize(query)), `${key}: search result does not contain query`);

  await input.fill("__tribuna_no_such_bookmaker__");
  await page.waitForTimeout(50);
  assert.equal(await visibleRows(page).count(), 0, `${key}: no-result search still displays rows`);

  await input.fill("");
  await page.waitForTimeout(50);
  assert.equal(await visibleRows(page).count(), items.length, `${key}: clearing search did not restore full list`);
}

async function exerciseViewport(page, items, key, viewport) {
  await page.setViewportSize(viewport);
  await page.reload({ waitUntil: "domcontentloaded" });
  await waitForWidget(page);
  await activateGroup(page, key);
  await assertRowsMatchGroup(page, items, key);
  await exerciseScroll(page, items, key);
  const dimensions = await page.locator(widgetSelector).evaluate((el) => ({
    clientWidth: el.clientWidth,
    scrollWidth: el.scrollWidth,
  }));
  assert.ok(dimensions.scrollWidth <= dimensions.clientWidth + 1, `${key}: widget overflows horizontally at ${viewport.width}px`);
}

async function main() {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  page.setDefaultTimeout(timeout);
  try {
    await page.goto(baseURL, { waitUntil: "domcontentloaded", timeout });
    await waitForWidget(page);
    const catalog = await readCatalog(page);
    const ru = groupItems(catalog, "ru");
    const best = groupItems(catalog, "best");

    // Desktop: verify both complete lists and the keyboard path.
    const ruButton = await activateGroup(page, "ru");
    await assertRowsMatchGroup(page, ru, "ru");
    await exerciseScroll(page, ru, "ru");
    await exerciseSearch(page, ru, "ru");
    const intlButton = page.locator('[data-bk-group="best"]');
    await intlButton.focus();
    await page.keyboard.press("Enter");
    await assertRowsMatchGroup(page, best, "best");
    assert.ok(await intlButton.evaluate((el) => document.activeElement === el), "best tab is not keyboard focusable");
    await exerciseScroll(page, best, "best");
    await exerciseSearch(page, best, "best");
    await ruButton.focus();
    await page.keyboard.press("Space");
    await assertRowsMatchGroup(page, ru, "ru after keyboard activation");

    // Mobile: repeat the layout/overflow checks for both catalogs.
    await exerciseViewport(page, ru, "ru", { width: 390, height: 844 });
    await exerciseViewport(page, best, "best", { width: 390, height: 844 });

    console.log(`bookmaker widget acceptance passed (${ru.length} RU + ${best.length} best entries)`);
  } finally {
    await browser.close();
  }
}

main().catch((error) => {
  console.error(`bookmaker widget acceptance failed: ${error.message}`);
  process.exitCode = 1;
});

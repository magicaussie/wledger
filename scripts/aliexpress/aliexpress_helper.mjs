#!/usr/bin/env node
/**
 * aliexpress_helper.mjs — AliExpress provider helper.
 *
 * Uses the same technique as github.com/sudheer-ranga/aliexpress-product-scraper:
 * Puppeteer + puppeteer-extra-plugin-stealth against the system Chromium, with
 * network-response interception to read the page's own JSON API payloads.
 *
 * Commands:
 *   node aliexpress_helper.mjs product <productId|itemUrl> [--timeout-ms 90000]
 *   node aliexpress_helper.mjs search <query> [--max 60] [--retries 4]
 *
 * Output: one JSON object on stdout. Logs go to stderr.
 */
import puppeteer from 'puppeteer-extra';
import StealthPlugin from 'puppeteer-extra-plugin-stealth';
puppeteer.use(StealthPlugin());

const UA = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36';

function log(...args) { console.error('[aliexpress]', ...args); }
function emit(obj) { process.stdout.write(JSON.stringify(obj)); }

function parseArgs(argv) {
    const out = { command: argv[0], arg: argv[1] };
    for (let i = 2; i < argv.length; i++) {
        const a = argv[i];
        if (a.startsWith('--')) {
            const key = a.replace(/^--/, '');
            const val = argv[i + 1];
            if (val !== undefined && !val.startsWith('--')) { out[key] = val; i++; }
            else { out[key] = true; }
        }
    }
    return out;
}

async function launch(opts) {
    return puppeteer.launch({
        headless: 'new',
        executablePath: opts.chromium || '/usr/bin/chromium',
        args: ['--no-sandbox', '--disable-dev-shm-usage', '--disable-blink-features=AutomationControlled', '--window-size=1680,960'],
    });
}

async function newPage(browser, opts) {
    const page = await browser.newPage();
    await page.setUserAgent(opts.ua || UA);
    await page.setViewport({ width: 1680, height: 960 });
    const timeout = Number(opts['timeout-ms'] || 90000);
    page.setDefaultTimeout(timeout);
    page.setDefaultNavigationTimeout(timeout);
    return page;
}

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

/** Extract the itemList.content array from a real search page. */
function extractItemList(html) {
    const anchor = '"itemList":{"content":[';
    const i = html.indexOf(anchor);
    if (i === -1) return null;
    let depth = 0, inStr = false, esc = false, end = -1;
    const start = i + anchor.length - 1;
    for (let idx = start; idx < html.length; idx++) {
        const ch = html[idx];
        if (inStr) {
            if (esc) esc = false;
            else if (ch === '\\') esc = true;
            else if (ch === '"') inStr = false;
            continue;
        }
        if (ch === '"') inStr = true;
        else if (ch === '[' || ch === '{') depth++;
        else if (ch === ']' || ch === '}') {
            depth--;
            if (depth === 0) { end = idx + 1; break; }
        }
    }
    if (end === -1) return null;
    try { return JSON.parse(html.slice(start, end)); } catch { return null; }
}

/** Search command with retries (captcha roulette). */
async function doSearch(browser, opts) {
    const query = encodeURIComponent(String(opts.arg || '').trim());
    if (!query) { emit({ ok: false, error: 'missing query' }); return; }
    const url = `https://www.aliexpress.com/w/wholesale-${query}.html`;
    const retries = Number(opts.retries || 4);
    const page = await newPage(browser, opts);

    for (let attempt = 1; attempt <= retries; attempt++) {
        log(`search attempt ${attempt}/${retries}: ${url}`);
        try {
            await page.goto(url, { waitUntil: 'networkidle2', timeout: 60000 }).catch(() => {});
            await sleep(Number(opts.settle || 15000));
        } catch (e) { log('goto error', String(e).slice(0, 120)); }

        const html = await page.content().catch(() => '');
        if (html.includes('CAPTCHA') || html.includes('punish') || html.includes('x5sec')) {
            log('captcha/punish page, retrying');
            continue;
        }
        const cards = extractItemList(html);
        if (cards && cards.length) {
            const results = cards
                .filter(c => c && (c.productId || c.redirectedId))
                .map(c => {
                    const id = String(c.productId || c.redirectedId);
                    const prices = c.prices || {};
                    const sale = prices.salePrice || {};
                    const orig = prices.originalPrice || {};
                    const title = (c.title || {}).displayTitle || '';
                    const img = (c.image || {}).imgUrl || '';
                    return {
                        id,
                        name: title,
                        sale_price: sale.minPrice != null ? sale.minPrice : sale.value != null ? sale.value : null,
                        sale_price_text: sale.formattedPrice || '',
                        original_price: orig.minPrice != null ? orig.minPrice : orig.value != null ? orig.value : null,
                        original_price_text: orig.formattedPrice || '',
                        currency: sale.currencyCode || orig.currencyCode || 'AUD',
                        discount: sale.discount != null ? sale.discount : null,
                        image: img.startsWith('//') ? 'https:' + img : img,
                        url: `https://www.aliexpress.com/item/${id}.html`,
                        rating: (c.rating || c.tradeScore || null),
                        orders: c.tradeCount || null,
                    };
                });
            log(`parsed ${results.length} results`);
            emit({ ok: true, results });
            return;
        }
        log('no itemList yet, retrying');
    }
    emit({ ok: false, error: 'aliexpress block/captcha page after retries' });
}

/** Product command: navigate to the item, read the PDP JSON API response. */
async function doProduct(browser, opts) {
    let id = String(opts.arg || '').trim();
    if (!id) { emit({ ok: false, error: 'missing product id' }); return; }
    const m = id.match(/item\/(\d+)(?:\.html)?/);
    if (m) id = m[1];
    const url = `https://www.aliexpress.com/item/${id}.html`;
    const retries = Number(opts.retries || 3);

    for (let attempt = 1; attempt <= retries; attempt++) {
        const page = await newPage(browser, opts);
        let pdp = null;
        page.on('response', async (res) => {
            if (res.url().includes('mtop.aliexpress.pdp.pc.query')) {
                try {
                    const t = await res.text().catch(() => '');
                    const m2 = t.match(/\((\{.*\})\)\s*$/);
                    if (!m2) return;
                    const obj = JSON.parse(m2[1]);
                    if (obj.data && obj.data.result) pdp = obj.data.result;
                } catch { /* ignore */ }
            }
        });
        log(`product attempt ${attempt}/${retries}: ${url}`);
        try {
            await page.goto(url, { waitUntil: 'networkidle2', timeout: 60000 }).catch(() => {});
            await sleep(Number(opts.settle || 12000));
        } catch (e) { log('goto error', String(e).slice(0, 120)); }

        const html = await page.content().catch(() => '');
        const blocked = html.includes('CAPTCHA') || html.includes('punish');
        if (pdp) {
            const detail = mapDetail(id, pdp);
            emit({ ok: true, product: detail });
            await page.close();
            return;
        }
        if (blocked) { log('captcha page, retrying'); await page.close(); continue; }
        log('no PDP response yet, retrying');
        await page.close();
    }
    emit({ ok: false, error: 'aliexpress block/captcha or no PDP data' });
}

function mapDetail(id, r) {
    const title = (r.PRODUCT_TITLE || {}).text || '';
    const priceMod = r.PRICE || {};
    const skuInfo = priceMod.skuIdStrPriceInfoMap || priceMod.skuPriceInfoMap || {};

    // Pick the first sku's price info (or the target sku).
    const selected = priceMod.selectedSkuId;
    let firstSku = null;
    if (selected != null && skuInfo[String(selected)]) firstSku = skuInfo[String(selected)];
    if (!firstSku) firstSku = Object.values(skuInfo)[0] || priceMod.targetSkuPriceInfo || null;

    let sale = null, orig = null, currency = 'AUD', discount = null;
    if (firstSku) {
        const op = firstSku.originalPrice || firstSku.skuPriceAmount || {};
        if (op.value != null) orig = op.value;
        currency = op.currency || firstSku.currencyCode || currency;
        // sale price present in several forms
        const sp = firstSku.salePrice || firstSku.salePriceString || firstSku.price || null;
        if (typeof sp === 'object' && sp.value != null) sale = sp.value;
        else if (typeof sp === 'string') {
            const m = sp.match(/(\d+(?:\.\d+)?)/);
            if (m) sale = parseFloat(m[1]);
        }
        if (firstSku.discount) discount = String(firstSku.discount);
    }

    const header = r.HEADER_IMAGE_PC || {};
    const images = (header.imagePathList || header.mainImages || [])
        .map(u => { const s = String(u); return s.startsWith('//') ? 'https:' + s : s; })
        .filter(u => u.startsWith('http'));

    const rating = r.PC_RATING || {};
    const shop = r.SHOP_CARD_PC || {};
    const desc = r.DESC || {};

    // Specs
    const specs = [];
    const prop = r.PRODUCT_PROP_PC || {};
    const seen = new Set();
    const push = (name, value) => {
        const key = name + '\u0000' + value;
        if (!seen.has(key)) { seen.add(key); specs.push({ attrName: name, attrValue: value }); }
    };
    (prop.showedProps || []).forEach(s => { if (s.attrName && s.attrValue) push(s.attrName, s.attrValue); });
    (prop.outerProps || []).forEach(s => { if (s.attrName && s.attrValue) push(s.attrName, s.attrValue); });
    (prop.singlePropComponentList || []).forEach(p => (p.propList || []).forEach(s => { if (s.name && s.value) push(s.name, s.value); }));

    return {
        id: String(id),
        title,
        images,
        sale_price: sale,
        original_price: orig,
        currency,
        discount,
        rating: rating.averageStar != null ? rating.averageStar : (rating.totalStar != null ? rating.totalStar : null),
        rating_count: rating.totalStartCount != null ? rating.totalStartCount : null,
        store_name: shop.storeName || shop.storeNameText || null,
        description_url: desc.msiteDescUrl || null,
        specs,
        url: `https://www.aliexpress.com/item/${id}.html`,
    };
}

async function main() {
    const opts = parseArgs(process.argv.slice(2));
    let browser;
    try {
        browser = await launch(opts);
        if (opts.command === 'search') await doSearch(browser, opts);
        else if (opts.command === 'product') await doProduct(browser, opts);
        else emit({ ok: false, error: 'unknown command ' + opts.command });
    } catch (e) {
        emit({ ok: false, error: String(e && e.message || e).slice(0, 300) });
    } finally {
        if (browser) await browser.close().catch(() => {});
    }
}

main();

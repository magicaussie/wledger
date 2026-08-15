#!/usr/bin/env python3
"""Amazon.com.au helper for the WLEDger amazon supplier provider.

This script is a thin JSON bridge between the Go application and amzpy. It
uses curl_cffi browser impersonation (via amzpy) to fetch and parse
amazon.com.au search results and product pages without an API key.

Protocol:
    python3 amazon_helper.py search "<query>" [--pages N] [--country com.au]
    python3 amazon_helper.py product "<ASIN>" [--country com.au]

Output is always a single JSON object on stdout:
    {"ok": true, "results": [ {...}, ... ]}   for search
    {"ok": true, "product": {...}}            for product
    {"ok": false, "error": "message"}         on any failure

Logs and diagnostics go to stderr so stdout stays machine-readable.
"""

import argparse
import json
import re
import sys
from contextlib import redirect_stdout

try:
    from amzpy import AmazonScraper
    from bs4 import BeautifulSoup
except ImportError as e:
    print(json.dumps({"ok": False, "error": f"missing dependency: {e}"}))
    sys.exit(0)

BLOCK_MARKERS = (
    "captcha",
    "api-services-support@amazon.com",
    "enter the characters you see below",
    "sorry, we just need to make sure you're not a robot",
    "we need to verify you're a human",
    "unusual traffic from your computer network",
)

DEFAULT_COUNTRY = "com.au"

CURRENCY_BY_COUNTRY = {
    "com.au": "AUD",
    "com": "USD",
    "co.uk": "GBP",
    "ca": "CAD",
    "de": "EUR",
    "fr": "EUR",
    "it": "EUR",
    "es": "EUR",
    "co.jp": "JPY",
    "in": "INR",
}


def _log(msg):
    sys.stderr.write("[amazon_helper] " + str(msg) + "\n")


def _blocked(text):
    lower = (text or "").lower()
    return any(m in lower for m in BLOCK_MARKERS)


def _emit(obj):
    print(json.dumps(obj))
    sys.stdout.flush()


def _make_scraper(country, proxies=None):
    scraper = AmazonScraper(
        country_code=country,
        impersonate="chrome",
        proxies=proxies,
    )
    scraper.config(
        MAX_RETRIES=2,
        REQUEST_TIMEOUT=30,
        DELAY_BETWEEN_REQUESTS=(2, 4),
    )
    return scraper


def _currency(country):
    return CURRENCY_BY_COUNTRY.get(country, "AUD")


def _canonical_url(country, asin):
    return "https://www.amazon.%s/dp/%s" % (country, asin)


def _clean(text):
    if text is None:
        return ""
    return re.sub(r"\s+", " ", text).strip()


def _run_search(country, query, pages, proxies):
    with redirect_stdout(sys.stderr):
        scraper = _make_scraper(country, proxies)
        raw = scraper.search_products(query=query, max_pages=pages)
    if raw is None:
        raise RuntimeError("amazon returned no data (possibly blocked)")
    results = []
    for item in raw:
        if isinstance(item, dict):
            item = item.copy()
            if item.get("url"):
                item["url"] = _canonical_url(country, item.get("asin") or "")
            results.append(item)
    if not results:
        _log("no products found for %r" % query)
    return results


def _parse_product(html, url, country):
    soup = BeautifulSoup(html, "lxml")
    p = {"url": url}

    title = soup.select_one("#productTitle")
    p["title"] = _clean(title.get_text()) if title else ""

    brand = soup.select_one("#bylineInfo")
    if brand:
        text = _clean(brand.get_text())
        m = re.search(r"visit the (.+?) store", text, re.I)
        if m:
            p["brand"] = m.group(1).strip()
        else:
            p["brand"] = text

    offscreen = soup.select_one("span.a-price span.a-offscreen")
    if offscreen:
        text = _clean(offscreen.get_text())
        m = re.search(r"(\d+\.?\d*)", text.replace(",", ""))
        if m:
            p["price"] = float(m.group(1))
            p["currency"] = _currency(country)

    img = soup.select_one("#landingImage") or soup.select_one("#imgBlkFront")
    if img:
        p["img_url"] = img.get("src") or img.get("data-old-hires")

    rating = soup.select_one("#acrPopover span.a-icon-alt")
    if rating:
        m = re.search(r"(\d+\.?\d*)", rating.get_text())
        if m:
            p["rating"] = float(m.group(1))

    reviews = soup.select_one("#acrCustomerReviewText")
    if reviews:
        m = re.search(r"([\d,]+)", reviews.get_text())
        if m:
            p["review_count"] = int(m.group(1).replace(",", ""))

    bullets = []
    for li in soup.select("#feature-bullets li span.a-list-item"):
        text = _clean(li.get_text())
        if text:
            bullets.append(text)
    if bullets:
        p["bullets"] = bullets

    specs = []
    for tr in soup.select(
        "#productDetails_techSpec_section_1 tr, #productDetails_detailBullets_sections1 tr"
    ):
        th = tr.select_one("th")
        td = tr.select_one("td")
        if th and td:
            name = _clean(th.get_text())
            value = _clean(td.get_text())
            if name and value:
                specs.append({"name": name, "value": value})
    if specs:
        p["specs"] = specs

    avail = soup.select_one("#availability span")
    if avail:
        p["availability"] = _clean(avail.get_text())

    seller = soup.select_one("#sellerProfileTriggerId")
    if seller:
        p["seller"] = _clean(seller.get_text())
    else:
        sold_by = soup.select_one(
            "#merchant-info, #tabular-buybox .tabular-buybox-text[data-imported]"
        )
        if sold_by:
            p["seller"] = _clean(sold_by.get_text())

    return p


def _run_product(country, asin, proxies):
    url = _canonical_url(country, asin)
    with redirect_stdout(sys.stderr):
        scraper = _make_scraper(country, proxies)
        resp = scraper.session.get(url)
    if resp is None:
        raise RuntimeError("amazon product request failed")
    text = resp.text or ""
    if _blocked(text):
        raise RuntimeError("amazon captcha detected")
    p = _parse_product(text, url, country)
    p["asin"] = asin
    if not p.get("title"):
        raise RuntimeError("could not parse product page for %s" % asin)
    return p


def _make_proxies(proxy_url):
    if not proxy_url:
        return None
    return {"http": proxy_url, "https": proxy_url}


def main(argv=None):
    ap = argparse.ArgumentParser(prog="amazon_helper")
    ap.add_argument("command", choices=["search", "product"])
    ap.add_argument("arg", help="search query or ASIN")
    ap.add_argument("--pages", type=int, default=1)
    ap.add_argument("--country", default=DEFAULT_COUNTRY)
    ap.add_argument("--proxy-url", default=None)
    args = ap.parse_args(argv)

    country = args.country.strip().lower() or DEFAULT_COUNTRY
    asin_re = re.compile(r"^[A-Z0-9]{10}$")

    try:
        if args.command == "search":
            query = args.arg.strip()
            if not query:
                raise RuntimeError("empty search query")
            results = _run_search(
                country, query, max(1, args.pages), _make_proxies(args.proxy_url)
            )
            _emit({"ok": True, "results": results})
        else:
            asin = args.arg.strip().upper()
            if not asin_re.match(asin):
                raise RuntimeError("invalid ASIN %r" % args.arg)
            product = _run_product(country, asin, _make_proxies(args.proxy_url))
            _emit({"ok": True, "product": product})
    except Exception as e:
        _log("error: %s" % e)
        _emit({"ok": False, "error": str(e)})


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""
Spotlight Stores Australia product search/detail helper.
Uses Selenium to bypass AWS WAF challenge and extracts data from embedded JSON/HTML.
"""
import sys
import json
import re
import argparse
from urllib.parse import urljoin, urlparse

from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
from selenium.common.exceptions import TimeoutException, WebDriverException


BASE_URL = "https://www.spotlightstores.com"
SEARCH_URL = BASE_URL + "/search"


def create_driver():
    """Create a headless Chrome driver configured to bypass basic detection."""
    options = Options()
    options.add_argument("--headless")
    options.add_argument("--no-sandbox")
    options.add_argument("--disable-dev-shm-usage")
    options.add_argument("--disable-gpu")
    options.add_argument("--window-size=1920,1080")
    options.add_argument("--disable-blink-features=AutomationControlled")
    options.add_experimental_option("excludeSwitches", ["enable-automation"])
    options.add_experimental_option("useAutomationExtension", False)
    options.add_argument(
        "user-agent=Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 "
        "(KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"
    )
    driver = webdriver.Chrome(options=options)
    driver.execute_cdp_cmd(
        "Page.addScriptToEvaluateOnNewDocument",
        {"source": "Object.defineProperty(navigator, 'webdriver', {get: () => undefined})"},
    )
    driver.set_page_load_timeout(45)
    return driver


def wait_for_challenge(driver, timeout=30):
    """Wait for AWS WAF challenge to resolve by checking for product content."""
    try:
        WebDriverWait(driver, timeout).until(
            lambda d: "product-card" in d.page_source
            or "product-tile" in d.page_source
            or "__SRG_PRODUCT_DATA__" in d.page_source
            or len(d.page_source) > 50000
        )
    except TimeoutException:
        pass


def is_challenge_page(html: str) -> bool:
    """Detect AWS WAF challenge page."""
    html_lower = html.lower()
    return any(
        term in html_lower
        for term in [
            "aws waf",
            "challenge-container",
            "verify you are human",
            "enable javascript",
            "gokuprops",
        ]
    )


def parse_price_text(text: str) -> dict:
    """Parse price text like '$8 per metre' into structured data."""
    result = {
        "full_price": None,
        "sale_price": None,
        "vip_price": None,
        "currency": "AUD",
        "unit": "each",
        "raw_text": text.strip(),
    }
    text_lower = text.lower()
    if "per metre" in text_lower:
        result["unit"] = "per metre"
    elif "per pack" in text_lower:
        result["unit"] = "per pack"
    elif "each" in text_lower:
        result["unit"] = "each"

    import re

    prices = re.findall(r"\$(\d+(?:\.\d+)?)", text)
    if prices:
        prices_float = [float(p) for p in prices]
        if "sale" in text_lower or "hot buy" in text_lower:
            result["sale_price"] = min(prices_float)
            result["full_price"] = max(prices_float) if len(prices_float) > 1 else None
        elif "vip" in text_lower:
            result["vip_price"] = min(prices_float)
            result["full_price"] = max(prices_float) if len(prices_float) > 1 else None
        else:
            result["full_price"] = prices_float[0]
    return result


def extract_search_results(html: str, base_url: str) -> list:
    """Parse product cards from search results HTML."""
    from lxml import html as lxml_html

    tree = lxml_html.fromstring(html)
    results = []

    # Product cards have data-product-id on wrapper div
    cards = tree.xpath("//div[@data-product-id]")
    for card in cards:
        pid = card.get("data-product-id", "").strip()
        if not pid:
            continue

        # Title
        title_el = card.xpath(
            ".//*[contains(@class, 'product-name') or contains(@class, 'product-title') or contains(@class, 'title')]//text()"
        )
        title = " ".join(t.strip() for t in title_el if t.strip())
        if not title:
            # Fallback: look for any text that looks like a product name
            all_text = card.xpath(".//text()")
            title = " ".join(t.strip() for t in all_text if len(t.strip()) > 10)[:200]

        # Price
        price_el = card.xpath(".//*[contains(@class, 'price')]//text()")
        price_text = " ".join(p.strip() for p in price_el if p.strip())
        price_data = parse_price_text(price_text) if price_text else {}

        # Link
        link_el = card.xpath(".//a[@href]")
        href = link_el[0].get("href") if link_el else ""
        if href and not href.startswith("http"):
            href = urljoin(base_url, href)

        # Image
        img_el = card.xpath(".//img[@src]")
        img_url = img_el[0].get("src") if img_el else ""
        if img_url and not img_url.startswith("http"):
            img_url = urljoin(base_url, img_url)

        # Category (from breadcrumb or category elements)
        cat_el = card.xpath(".//*[contains(@class, 'category')]//text()")
        category = " ".join(c.strip() for c in cat_el if c.strip())

        results.append(
            {
                "id": pid,
                "sku": pid,
                "name": title,
                "url": href,
                "image": img_url,
                "full_price": price_data.get("full_price"),
                "sale_price": price_data.get("sale_price"),
                "vip_price": price_data.get("vip_price"),
                "currency": price_data.get("currency", "AUD"),
                "price_text": price_data.get("raw_text", ""),
                "unit": price_data.get("unit", "each"),
                "category": category,
            }
        )

    return results


def extract_detail_product(html: str, url: str) -> dict:
    """Parse product detail from embedded JSON and HTML."""
    from lxml import html as lxml_html

    tree = lxml_html.fromstring(html)

    # Try embedded JSON first
    match = re.search(r"window\.__SRG_PRODUCT_DATA__\s*=\s*({.*?});", html, re.DOTALL)
    if match:
        try:
            data = json.loads(match.group(1))
            return parse_embedded_json(data, url)
        except json.JSONDecodeError:
            pass

    # Fallback to HTML parsing
    return parse_detail_html(tree, url)


def parse_embedded_json(data: dict, url: str) -> dict:
    """Parse product from __SRG_PRODUCT_DATA__ JSON."""
    # Extract price info
    price_data = data.get("price", {}) or {}
    from_reg = data.get("fromRegularPrice", {}) or {}
    to_reg = data.get("toRegularPrice", {}) or {}
    from_vip = data.get("fromVipPrice", {}) or {}
    to_vip = data.get("toVipPrice", {}) or {}

    full_price = from_reg.get("value") or price_data.get("value")
    sale_price = None  # Spotlight uses regular/sale differently
    vip_price = from_vip.get("value") or to_vip.get("value")

    # Determine unit
    unit = "each"
    if data.get("isMeteredProduct"):
        unit = "per metre"
    price_text = f"${full_price:.2f}" if full_price else ""
    if unit != "each":
        price_text += f" {unit}"

    # Images
    images = []
    for img in data.get("images", []):
        img_url = img.get("url")
        if img_url and not img_url.startswith("http"):
            img_url = urljoin(BASE_URL, img_url)
        if img_url:
            images.append(img_url)

    # Categories
    category = ""
    if data.get("categoryPath"):
        category = data["categoryPath"][-1].get("name", "")
    elif data.get("categories"):
        category = data["categories"][-1].get("name", "")

    # Availability
    stock = data.get("stock", {})
    availability = stock.get("stockLevelStatus", {}).get("code", "")
    in_stock_product = data.get("inStockProduct", False)
    purchasable = data.get("purchasable", False)
    stock_level = stock.get("stockLevel", 0)
    
    # For metered products, inStockProduct might be false but they're purchasable with stock
    if data.get("isMeteredProduct"):
        if purchasable and stock_level > 0:
            availability = "inStock"
    elif in_stock_product:
        availability = "inStock"
    elif purchasable and stock_level > 0:
        availability = "inStock"

    # Specifications
    specs = {}
    for cls in data.get("classifications", []):
        for feat in cls.get("features", []):
            name = feat.get("name")
            vals = feat.get("featureValues", [])
            if name and vals:
                specs[name] = vals[0].get("value", "")

    # Description
    description = data.get("description", "") or data.get("shortDescription", "")

    return {
        "id": data.get("code", ""),
        "sku": data.get("baseProduct", data.get("code", "")),
        "name": data.get("name", ""),
        "url": url,
        "images": images,
        "full_price": full_price,
        "sale_price": sale_price,
        "vip_price": vip_price,
        "currency": price_data.get("currencyIso", "AUD"),
        "price_text": price_text,
        "unit": unit,
        "availability": availability,
        "category": category,
        "description": description,
        "specifications": specs,
        "ean": data.get("ean", ""),
        "rating": data.get("averageRating"),
        "review_count": data.get("numberOfReviews"),
        "brand": data.get("brandCategory", {}).get("name", "") if data.get("brandCategory") else "",
    }


def parse_detail_html(tree, url: str) -> dict:
    """Fallback HTML parsing for detail page."""
    # Title
    title_el = tree.xpath("//h1[@itemprop='name'] | //h1[contains(@class, 'product')] | //h1")
    title = title_el[0].text_content().strip() if title_el else ""

    # Price
    price_el = tree.xpath("//*[contains(@class, 'price-regular')]//text() | //*[@itemprop='price']")
    price_text = " ".join(p.strip() for p in price_el if p.strip())
    price_data = parse_price_text(price_text) if price_text else {}

    # Images
    img_el = tree.xpath("//img[@itemprop='image'] | //*[contains(@class, 'product-hero')]//img | //*[contains(@class, 'gallery')]//img")
    images = []
    for img in img_el:
        src = img.get("src") or img.get("data-src")
        if src:
            if not src.startswith("http"):
                src = urljoin(BASE_URL, src)
            images.append(src)

    # Availability
    avail_el = tree.xpath("//*[@itemprop='availability'] | //*[contains(@class, 'stock')] | //*[contains(@class, 'availability')]")
    availability = " ".join(a.text_content().strip() for a in avail_el if a.text_content().strip())

    # Description
    desc_el = tree.xpath("//*[@itemprop='description'] | //*[contains(@class, 'description')]")
    description = desc_el[0].text_content().strip() if desc_el else ""

    return {
        "id": "",
        "sku": "",
        "name": title,
        "url": url,
        "images": images,
        "full_price": price_data.get("full_price"),
        "sale_price": price_data.get("sale_price"),
        "vip_price": price_data.get("vip_price"),
        "currency": price_data.get("currency", "AUD"),
        "price_text": price_data.get("raw_text", ""),
        "unit": price_data.get("unit", "each"),
        "availability": availability,
        "category": "",
        "description": description,
        "specifications": {},
    }


def search_products(query: str, max_pages: int = 1) -> list:
    """Search for products and return list of results."""
    driver = create_driver()
    all_results = []

    try:
        for page in range(max_pages):
            page_url = f"{SEARCH_URL}?text={query}"
            if page > 0:
                page_url += f"&page={page}"

            driver.get(page_url)
            wait_for_challenge(driver)

            html = driver.page_source
            if is_challenge_page(html):
                return {"error": "spotlight challenge page detected"}

            results = extract_search_results(html, BASE_URL)
            if not results:
                break
            all_results.extend(results)

            # Check for pagination
            if "page-link" not in html or "selected" not in html:
                break

    except WebDriverException as e:
        return {"error": f"selenium error: {e}"}
    finally:
        driver.quit()

    return all_results


def get_product_detail(url: str) -> dict:
    """Get detailed product information from product page."""
    if not url.startswith("http"):
        url = urljoin(BASE_URL, url)

    driver = create_driver()
    try:
        driver.get(url)
        wait_for_challenge(driver)

        html = driver.page_source
        if is_challenge_page(html):
            return {"error": "spotlight challenge page detected"}

        return extract_detail_product(html, url)
    except WebDriverException as e:
        return {"error": f"selenium error: {e}"}
    finally:
        driver.quit()


def main():
    parser = argparse.ArgumentParser(description="Spotlight Stores product helper")
    subparsers = parser.add_subparsers(dest="command", required=True)

    search_parser = subparsers.add_parser("search", help="Search products")
    search_parser.add_argument("query", help="Search query")
    search_parser.add_argument("--pages", type=int, default=1, help="Max pages to fetch")

    detail_parser = subparsers.add_parser("product", help="Get product detail")
    detail_parser.add_argument("url", help="Product URL or product ID")

    args = parser.parse_args()

    if args.command == "search":
        results = search_products(args.query, args.pages)
        if isinstance(results, dict) and "error" in results:
            print(json.dumps({"ok": False, "error": results["error"]}))
        else:
            print(json.dumps({"ok": True, "results": results}))
    elif args.command == "product":
        result = get_product_detail(args.url)
        if "error" in result:
            print(json.dumps({"ok": False, "error": result["error"]}))
        else:
            print(json.dumps({"ok": True, "product": result}))


if __name__ == "__main__":
    main()
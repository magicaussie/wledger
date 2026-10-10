// Attach the session CSRF token to every HTMX request as the X-CSRF-Token
// header, sourced from the <meta name="csrf-token"> tag rendered for
// authenticated users. This protects state-changing HTMX POSTs (for example the
// LED locate and global-off actions) without per-element markup.
(function () {
  function token() {
    var meta = document.querySelector('meta[name="csrf-token"]');
    return meta ? meta.getAttribute("content") || "" : "";
  }

  document.addEventListener("htmx:configRequest", function (evt) {
    var t = token();
    if (t) {
      evt.detail.headers["X-CSRF-Token"] = t;
    }
  });
})();

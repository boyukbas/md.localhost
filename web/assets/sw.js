/* md.localhost service worker.
 *
 * It exists for one case: the viewer has been pinned to the taskbar, the user
 * clicks it, and the local server is not running. Without a worker that is a
 * browser error page inside a window with no address bar; with one it is the
 * viewer's own page, which can start the server again.
 *
 * It caches nothing else. Documents are read from disk on every request and
 * swapped into the page in place when the file changes, so a cached document
 * is the one failure this viewer must never have — anything that is not a
 * failed navigation goes straight to the network, untouched.
 *
 * Navigation preload matters here and is not an optimisation. A worker that
 * forwards a navigation with fetch(request) becomes the request's initiator,
 * and the server then sees Sec-Fetch-Site: same-origin / Dest: empty instead of
 * the none / document a taskbar launch actually sends. The server reads exactly
 * those headers to decide whether a page was opened by the user, so a forwarded
 * navigation would be refused. With preload the browser issues the request
 * itself and the original headers arrive intact. */

var CACHE = "mdl-offline-v1";
var OFFLINE = "/offline.html";

// The server injects the offline page here, so the worker can still answer when
// CacheStorage is unavailable — some managed profiles refuse it outright.
var OFFLINE_HTML = "__OFFLINE_HTML__";

function offlineResponse() {
  return caches.match(OFFLINE)
    .then(function (hit) { return hit || fallback(); })
    .catch(fallback);
}

function fallback() {
  return new Response(OFFLINE_HTML, {
    status: 503,
    headers: { "Content-Type": "text/html; charset=utf-8" }
  });
}

self.addEventListener("install", function (e) {
  // Precaching is an optimisation, not a precondition: a rejected install makes
  // the whole registration redundant, and then the worker is not there for the
  // one case it exists for.
  e.waitUntil(
    caches.open(CACHE)
      .then(function (c) { return c.add(new Request(OFFLINE, { cache: "reload" })); })
      .catch(function () { /* fall back to the injected copy */ })
      .then(function () { return self.skipWaiting(); })
  );
});

self.addEventListener("activate", function (e) {
  e.waitUntil(
    Promise.resolve()
      .then(function () {
        if (self.registration.navigationPreload) {
          return self.registration.navigationPreload.enable().catch(function () {});
        }
      })
      .then(function () {
        return caches.keys().then(function (keys) {
          return Promise.all(keys.map(function (k) { return k === CACHE ? null : caches.delete(k); }));
        });
      })
      .catch(function () { /* nothing cached, nothing to clean */ })
      .then(function () { return self.clients.claim(); })
  );
});

self.addEventListener("fetch", function (e) {
  if (e.request.mode !== "navigate") { return; }

  e.respondWith(
    Promise.resolve(e.preloadResponse)
      .then(function (preloaded) {
        // Preferred: the browser made this request, so its own headers are on
        // it. Only if preload is unavailable does the worker fetch it itself.
        return preloaded || fetch(e.request);
      })
      // Only a dead server lands here: an HTTP error, including the 403 a stale
      // session gets, is a real response and passes straight through.
      .catch(offlineResponse)
  );
});

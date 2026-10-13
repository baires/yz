// yz-sites: serves static sites uploaded by `yz site` from R2.
// /s/<id>/<path> maps to sites/<id>/<path> in the bound bucket.
const TYPES = {
  html: "text/html; charset=utf-8", htm: "text/html; charset=utf-8",
  css: "text/css; charset=utf-8", js: "text/javascript; charset=utf-8",
  mjs: "text/javascript; charset=utf-8", json: "application/json",
  map: "application/json", txt: "text/plain; charset=utf-8",
  xml: "application/xml", svg: "image/svg+xml", png: "image/png",
  jpg: "image/jpeg", jpeg: "image/jpeg", gif: "image/gif", webp: "image/webp",
  avif: "image/avif", ico: "image/x-icon", wasm: "application/wasm",
  woff: "font/woff", woff2: "font/woff2", ttf: "font/ttf", otf: "font/otf",
  mp4: "video/mp4", webm: "video/webm", mp3: "audio/mpeg", pdf: "application/pdf",
  webmanifest: "application/manifest+json",
};

const ID = /^[0-9A-Za-z]{16}$/;

function ext(key) {
  const name = key.slice(key.lastIndexOf("/") + 1);
  const dot = name.lastIndexOf(".");
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : "";
}

async function serve(env, request, key, status = 200) {
  const object = await env.BUCKET.get(key);
  if (!object) return null;
  const headers = new Headers();
  object.writeHttpMetadata(headers);
  headers.set("etag", object.httpEtag);
  const type = TYPES[ext(key)];
  if (type) headers.set("content-type", type);
  headers.set("x-content-type-options", "nosniff");
  headers.set("cache-control", ext(key) === "html" ? "no-cache" : "public, max-age=300");
  const body = request.method === "HEAD" ? null : object.body;
  return new Response(body, { status, headers });
}

export default {
  async fetch(request, env) {
    if (request.method !== "GET" && request.method !== "HEAD") {
      return new Response("method not allowed", { status: 405, headers: { allow: "GET, HEAD" } });
    }
    const url = new URL(request.url);
    const parts = url.pathname.split("/");
    // ["", "s", "<id>", ...rest]
    if (parts[1] !== "s" || !ID.test(parts[2] || "")) {
      return new Response("not found", { status: 404 });
    }
    const id = parts[2];
    if (parts.length === 3) {
      url.pathname += "/";
      return Response.redirect(url.toString(), 301);
    }
    let rest;
    try {
      rest = parts.slice(3).map(decodeURIComponent).join("/");
    } catch {
      return new Response("bad request", { status: 400 });
    }
    if (rest.split("/").some((seg) => seg === ".." || seg === ".")) {
      return new Response("bad request", { status: 400 });
    }
    const prefix = `sites/${id}/`;
    if (rest === "" || rest.endsWith("/")) rest += "index.html";
    const hit = await serve(env, request, prefix + rest);
    if (hit) return hit;
    if (!ext(rest)) {
      // /about -> /about/index.html, then SPA fallback to the root index.
      const dir = await serve(env, request, prefix + rest + "/index.html");
      if (dir) return dir;
      const spa = await serve(env, request, prefix + "index.html");
      if (spa) return spa;
    }
    const missing = await serve(env, request, prefix + "404.html", 404);
    return missing || new Response("not found", { status: 404 });
  },
};

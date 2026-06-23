// generic_renderer.js — smart recursive renderer for structured data.
//
// Used for: inputs, outputs, plan artifacts, evidence — anything that
// isn't a specialized tool-call card.
//
// Rules:
// - No "object · N keys" / "list · N items" labels; render content directly.
// - Empty values render as "(empty)" in --muted-2.
// - Booleans-all-true objects collapse to "✓ a, b, c" muted line.
// - Nested objects render inline if ≤3 small primitive children; otherwise indent.
// - Duplicate values within a single render call get muted styling + "(same as <key>)" marker.

(function (global) {
  function renderValue(v, opts) {
    opts = opts || {};
    const seen = opts._seen || new Map(); // value-hash → first-key path
    const path = opts._path || [];

    if (v === null || v === undefined || v === "" ||
        (Array.isArray(v) && v.length === 0) ||
        (isPlainObject(v) && Object.keys(v).length === 0)) {
      const el = doc("span", "gr-empty");
      el.textContent = "(empty)";
      return el;
    }

    if (typeof v === "string") return renderString(v);
    if (typeof v === "number" || typeof v === "boolean") {
      const el = doc("span", "gr-prim");
      el.textContent = String(v);
      return el;
    }
    if (Array.isArray(v)) return renderArray(v, { ...opts, _seen: seen, _path: path });
    if (isPlainObject(v)) return renderObject(v, { ...opts, _seen: seen, _path: path });

    const el = doc("span", "gr-prim");
    el.textContent = String(v);
    return el;
  }

  function renderString(s) {
    if (s.length <= 80 && !s.includes("\n")) {
      const el = doc("span", "gr-string");
      el.textContent = s;
      return el;
    }
    const el = doc("pre", "gr-string-block");
    el.textContent = s;
    return el;
  }

  function renderArray(arr, opts) {
    // All-string short list → comma-separated inline.
    if (arr.every(x => typeof x === "string" && x.length <= 40)) {
      const el = doc("span", "gr-list-inline");
      el.textContent = arr.join(", ");
      return el;
    }
    const ul = doc("ul", "gr-list");
    for (let i = 0; i < arr.length; i++) {
      const li = doc("li", "gr-list-item");
      li.appendChild(renderValue(arr[i], { ...opts, _path: opts._path.concat("[" + i + "]") }));
      ul.appendChild(li);
    }
    return ul;
  }

  function renderObject(obj, opts) {
    const keys = Object.keys(obj);

    // Booleans-all-true → muted summary
    if (keys.length > 0 && keys.every(k => obj[k] === true)) {
      const el = doc("div", "gr-bool-summary");
      el.textContent = "✓ " + keys.join(", ");
      return el;
    }

    // ≤3 small primitive children → inline
    const allSmallPrim = keys.length <= 3 && keys.every(k => {
      const v = obj[k];
      if (v === null) return true;
      const t = typeof v;
      if (t === "number" || t === "boolean") return true;
      if (t === "string") return v.length <= 40 && !v.includes("\n");
      return false;
    });

    const container = doc(allSmallPrim ? "span" : "div", allSmallPrim ? "gr-obj-inline" : "gr-obj");

    for (let i = 0; i < keys.length; i++) {
      const k = keys[i];
      const v = obj[k];
      const row = doc(allSmallPrim ? "span" : "div", "gr-row");
      const keyEl = doc("span", "gr-key");
      keyEl.textContent = k;
      row.appendChild(keyEl);

      const valHash = hashVal(v);
      const seenAt = valHash ? opts._seen.get(valHash) : null;
      if (seenAt) {
        const muted = doc("span", "gr-dup");
        muted.textContent = "(same as " + seenAt + ")";
        row.appendChild(muted);
      } else {
        if (valHash) opts._seen.set(valHash, k);
        row.appendChild(renderValue(v, { ...opts, _path: opts._path.concat(k) }));
      }

      container.appendChild(row);
      if (allSmallPrim && i < keys.length - 1) {
        const sep = doc("span", "gr-sep");
        sep.textContent = ", ";
        container.appendChild(sep);
      }
    }

    return container;
  }

  function isPlainObject(v) {
    return v !== null && typeof v === "object" && v.constructor === Object;
  }

  function hashVal(v) {
    if (v === null || v === undefined || v === "") return null;
    if (typeof v === "string" && v.length < 4) return null;
    try { return JSON.stringify(v); } catch { return null; }
  }

  function doc(tag, cls) {
    const el = document.createElement(tag);
    if (cls) el.className = cls;
    return el;
  }

  global.GenericRenderer = { renderValue };
})(window);

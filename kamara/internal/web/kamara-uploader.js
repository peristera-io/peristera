// kamara-uploader — a self-contained drag-and-drop upload component that
// progressively enhances a server-rendered <form> it wraps: it adds a drop
// target and a native <progress> bar, and lets the existing form submit do
// the upload. Without JS the plain form still works (and stays the thing the
// a11y check evaluates); with JS you get drag-drop + progress.
//
// Kamara's UI is its first consumer. Extraction into an SDK is mostly
// lift-and-shift; the one coupling to note is the progress source (htmx's
// htmx:xhr:progress event) — an SDK build would swap that for its own XHR.
class KamaraUploader extends HTMLElement {
  connectedCallback() {
    // Enhance once — guard against a re-adopt re-running this and appending
    // a second <progress> (safe under today's reparse-per-swap layout, but
    // cheap insurance and correct for the planned SDK extraction).
    if (this._enhanced) return;
    this._enhanced = true;
    const form = this.querySelector("form");
    const input = this.querySelector('input[type="file"]');
    if (!form || !input) return;

    const bar = document.createElement("progress");
    bar.max = 100;
    bar.value = 0;
    bar.hidden = true;
    bar.className = "mt-2 block w-full";
    bar.setAttribute("aria-label", this.getAttribute("progress-label") || "Upload progress");
    form.appendChild(bar);

    form.addEventListener("htmx:xhr:progress", (e) => {
      bar.hidden = false;
      if (e.detail.lengthComputable) {
        bar.value = Math.round((e.detail.loaded / e.detail.total) * 100);
      }
    });
    form.addEventListener("htmx:afterRequest", () => {
      bar.hidden = true;
      bar.value = 0;
    });

    const ring = ["ring-2", "ring-brand"];
    const over = (on) => (e) => {
      e.preventDefault();
      this.classList.toggle(ring[0], on);
      this.classList.toggle(ring[1], on);
    };
    ["dragover", "dragenter"].forEach((ev) => this.addEventListener(ev, over(true)));
    ["dragleave", "drop"].forEach((ev) => this.addEventListener(ev, over(false)));
    this.addEventListener("drop", (e) => {
      if (e.dataTransfer.files.length) {
        input.files = e.dataTransfer.files;
        form.requestSubmit();
      }
    });
  }
}
customElements.define("kamara-uploader", KamaraUploader);

// Drawer glue (registered once): the details drawer lives in #drawer, a
// sibling of the #browser listing, so listing swaps don't clear it. Clear it
// whenever the listing changes (navigate/mutation) so it can't show stale
// metadata for a moved/deleted file; move focus into it when it opens; and
// let Escape dismiss it (the drawer is a non-modal region, so no focus trap).
function drawerEl() {
  return document.getElementById("drawer");
}
document.addEventListener("htmx:afterSwap", (e) => {
  const d = drawerEl();
  if (!d || !e.target) return;
  if (e.target.id === "browser") {
    d.innerHTML = ""; // stale-drawer guard
  } else if (e.target === d) {
    const region = d.querySelector("[data-drawer]");
    if (region) region.focus();
  }
});
document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  const d = drawerEl();
  if (d && d.innerHTML) d.innerHTML = "";
});
// Delegated close-drawer handler (#38: replaces an inline onclick so the
// pages need no script-src 'unsafe-inline').
document.addEventListener("click", (e) => {
  if (e.target.closest("[data-close-drawer]")) {
    const d = drawerEl();
    if (d) d.innerHTML = "";
  }
});

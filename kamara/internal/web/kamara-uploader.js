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

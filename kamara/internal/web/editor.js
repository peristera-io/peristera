// Auto-submit the WOPI form so the office engine loads in the iframe. External
// (not inline) so the page needs no script-src 'unsafe-inline' (#38).
document.addEventListener("DOMContentLoaded", () => {
  const form = document.getElementById("office_form");
  if (form) form.submit();
});

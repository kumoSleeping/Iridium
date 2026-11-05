// The theme before the first paint (a file, not inline: the page's CSP allows only its own scripts):
// ?theme=, then this device's choice, then the system's.
(function () {
  var q = new URLSearchParams(location.search).get("theme");
  var s = null;
  try {
    s = localStorage.getItem("theme");
  } catch (e) {}
  var pick = q || s || "auto";
  var dark = pick === "dark" || (pick === "auto" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
})();

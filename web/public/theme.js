// The theme before the first paint (a file, not inline: the page's CSP allows only its own scripts):
// ?theme=, then this device's choice, then the system's. The browser's bar (theme-color) in the
// page's paper, by the theme chosen rather than the system's (me.tsx applyTheme keeps it so).
(function () {
  var q = new URLSearchParams(location.search).get("theme");
  var s = null;
  try {
    s = localStorage.getItem("theme");
  } catch (e) {}
  var pick = q || s || "auto";
  var dark = pick === "dark" || (pick === "auto" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  document.querySelectorAll('meta[name="theme-color"]').forEach(function (m) {
    m.removeAttribute("media");
    m.content = dark ? "#121212" : "#fdfdfd";
  });
})();

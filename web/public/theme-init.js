// Applies the saved theme before the first paint (same storage key as
// next-themes), so a dark UI never flashes white while the bundle loads.
;(function () {
  try {
    var saved = localStorage.getItem("theme")
    var dark =
      saved === "dark" ||
      ((!saved || saved === "system") && window.matchMedia("(prefers-color-scheme: dark)").matches)
    var root = document.documentElement
    root.classList.toggle("dark", dark)
    root.style.colorScheme = dark ? "dark" : "light"
  } catch (e) {
    // storage can be unavailable; next-themes falls back to the system theme
  }
})()

// The color theme: "light", "dark", or "system" (follow the OS setting),
// stored per browser. The .dark class on <html> selects the dark colors
// (see app/globals.css).

export type Theme = "light" | "dark" | "system";

export const THEME_KEY = "travel-docs.theme";

/**
 * Runs in <head> before the first paint (see the Next.js guide "How to
 * prevent flash before hydration"): applies the saved theme, and keeps
 * following the OS setting while the choice is "system". Exposes
 * window.__setTheme for the theme switcher.
 */
export const themeScript = `(function(){
var k=${JSON.stringify(THEME_KEY)},m=matchMedia("(prefers-color-scheme: dark)"),c=document.documentElement;
function get(){try{return localStorage.getItem(k)||"system"}catch(e){return "system"}}
function apply(){var t=get();c.classList.toggle("dark",t==="dark"||(t==="system"&&m.matches));c.dataset.theme=t}
m.addEventListener("change",apply);
window.__setTheme=function(t){try{if(t==="system")localStorage.removeItem(k);else localStorage.setItem(k,t)}catch(e){}apply();dispatchEvent(new Event("themechange"))};
apply();
})()`;

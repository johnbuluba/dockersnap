// Two-mode theme toggle. Dark is the default; .light on <html> opts in.
// We persist via localStorage so the inline script in index.html can apply
// the choice before paint.
import { useEffect, useState } from "preact/hooks";

export type Theme = "dark" | "light";

const STORAGE_KEY = "dockersnap.theme";

function readInitial(): Theme {
  if (typeof document === "undefined") return "dark";
  return document.documentElement.classList.contains("light") ? "light" : "dark";
}

export function useTheme(): [Theme, (next: Theme) => void] {
  const [theme, setTheme] = useState<Theme>(readInitial);

  useEffect(() => {
    const root = document.documentElement;
    if (theme === "light") root.classList.add("light");
    else root.classList.remove("light");
    try {
      localStorage.setItem(STORAGE_KEY, theme);
    } catch {
      // Ignore storage errors (private mode, etc.) — the class change still works for the session.
    }
  }, [theme]);

  return [theme, setTheme];
}

import { useState } from "react";
import { Moon, Sun } from "lucide-react";

import { Button } from "@/components/ui/button";
import { applyTheme, resolveInitialTheme, writeThemeCookie, type Theme } from "@/lib/storage/themeCookie";

// The cookie (not React state) is the actual source of truth across page
// loads — this component's useState is just what re-renders the icon;
// main.tsx's applyTheme(resolveInitialTheme()) call at startup is what
// makes the choice stick before this component ever mounts.
export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(() => resolveInitialTheme());

  function toggle() {
    const next: Theme = theme === "dark" ? "light" : "dark";
    writeThemeCookie(next);
    applyTheme(next);
    setTheme(next);
  }

  return (
    <Button variant="ghost" size="icon" onClick={toggle} aria-label="Toggle theme">
      {theme === "dark" ? <Sun className="size-4" /> : <Moon className="size-4" />}
    </Button>
  );
}

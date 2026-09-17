import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
type Theme = "light" | "dark" | "system";
const ThemeContext = createContext<{
  theme: Theme;
  setTheme: (theme: Theme) => void;
}>({ theme: "system", setTheme: () => {} });
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, update] = useState<Theme>(() => {
    try {
      const value = localStorage.getItem("sbmgr-theme");
      if (value === "light" || value === "dark") return value;
    } catch {
      /* Storage may be unavailable. */
    }
    return "system";
  });
  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () =>
      document.documentElement.classList.toggle(
        "dark",
        theme === "dark" || (theme === "system" && media.matches),
      );
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [theme]);
  return (
    <ThemeContext.Provider
      value={{
        theme,
        setTheme: (value) => {
          update(value);
          try {
            localStorage.setItem("sbmgr-theme", value);
          } catch {
            /* Preference remains available in memory. */
          }
        },
      }}
    >
      {children}
    </ThemeContext.Provider>
  );
}
export const useTheme = () => useContext(ThemeContext);

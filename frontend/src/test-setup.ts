import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";
afterEach(cleanup);
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
  }),
});
Object.defineProperty(Element.prototype, "hasPointerCapture", {
  value: () => false,
  configurable: true,
});
Object.defineProperty(Element.prototype, "setPointerCapture", {
  value: () => {},
  configurable: true,
});
Object.defineProperty(Element.prototype, "releasePointerCapture", {
  value: () => {},
  configurable: true,
});
Object.defineProperty(Element.prototype, "scrollIntoView", {
  value: () => {},
  configurable: true,
});

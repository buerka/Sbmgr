import React from "react";
import ReactDOM from "react-dom/client";
import { Provider } from "react-redux";
import { createHashRouter, RouterProvider } from "react-router-dom";
import { setNonce } from "get-nonce";
import "@fontsource/inter/latin-400.css";
import "@fontsource/inter/latin-500.css";
import "@fontsource/inter/latin-600.css";
import "@fontsource/inter/latin-700.css";
import { ThemeProvider } from "./theme";
import { store, bootstrap } from "./store";
import { App } from "./App";
import "./styles.css";
// Radix's scroll lock creates a style element; use the server's existing CSP nonce.
const nonce = document.querySelector<HTMLMetaElement>(
  'meta[name="csp-nonce"]',
)?.content;
if (nonce && nonce !== "__CSP_NONCE__") setNonce(nonce);
const router = createHashRouter([{ path: "*", element: <App /> }]);
ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ThemeProvider>
      <Provider store={store}>
        <RouterProvider router={router} />
      </Provider>
    </ThemeProvider>
  </React.StrictMode>,
);
void bootstrap();

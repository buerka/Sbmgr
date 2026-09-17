import React from "react";
import ReactDOM from "react-dom/client";
import createCache from "@emotion/cache";
import { CacheProvider } from "@emotion/react";
import { CssBaseline, ThemeProvider } from "@mui/material";
import { Provider } from "react-redux";
import { HashRouter } from "react-router-dom";
import "@fontsource/roboto/latin-400.css";
import "@fontsource/roboto/latin-500.css";
import "@fontsource/roboto/latin-700.css";
import { theme } from "./theme";
import { store, bootstrap } from "./store";
import { App } from "./App";
import "./styles.css";

const nonce = document.querySelector<HTMLMetaElement>(
  'meta[name="csp-nonce"]',
)?.content;
const cache = createCache({
  key: "sbmgr",
  nonce: nonce === "__CSP_NONCE__" ? undefined : nonce,
});
ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <CacheProvider value={cache}>
      <ThemeProvider theme={theme}>
        <CssBaseline />
        <Provider store={store}>
          <HashRouter>
            <App />
          </HashRouter>
        </Provider>
      </ThemeProvider>
    </CacheProvider>
  </React.StrictMode>,
);
void bootstrap();

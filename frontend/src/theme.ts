import { createTheme } from "@mui/material/styles";

// The Cloudreve reference uses MUI, 12px shapes and compact, unelevated controls.
export const theme = createTheme({
  palette: {
    mode: "light",
    primary: { main: "#1976d2" },
    background: { default: "#f5f5f5", paper: "#fff" },
    text: { primary: "#292929", secondary: "#737373" },
    divider: "#e8e8e8",
  },
  shape: { borderRadius: 12 },
  typography: {
    fontFamily: 'Roboto, "Segoe UI", "Microsoft YaHei", sans-serif',
    fontSize: 13,
    h1: { fontSize: 24, fontWeight: 500 },
    h2: { fontSize: 16, fontWeight: 500 },
    h3: { fontSize: 14, fontWeight: 500 },
    button: { textTransform: "none", fontWeight: 500 },
    body2: { fontSize: 13 },
  },
  components: {
    MuiButton: {
      defaultProps: { disableElevation: true, size: "small" },
      styleOverrides: {
        root: { minHeight: 34, borderRadius: 8, textTransform: "none" },
      },
    },
    MuiIconButton: { defaultProps: { size: "small" } },
    MuiTextField: { defaultProps: { size: "small", variant: "outlined" } },
    MuiTooltip: { defaultProps: { enterDelay: 500 } },
    MuiListItemButton: { styleOverrides: { root: { borderRadius: 10 } } },
    MuiMenu: {
      styleOverrides: { paper: { borderRadius: 8 }, list: { padding: 4 } },
      defaultProps: { slotProps: { paper: { elevation: 3 } } },
    },
    MuiMenuItem: {
      styleOverrides: {
        root: { borderRadius: 6, margin: "1px 0", fontSize: 13, minHeight: 36 },
      },
    },
    MuiDialog: { styleOverrides: { paper: { borderRadius: 12 } } },
    MuiTableCell: {
      styleOverrides: {
        root: { borderColor: "#eeeeee", fontSize: 13, padding: "14px 18px" },
        head: {
          fontSize: 12,
          fontWeight: 400,
          color: "#737373",
          background: "#fafafa",
        },
      },
    },
    MuiChip: {
      defaultProps: { size: "small" },
      styleOverrides: { root: { borderRadius: 6, height: 24, fontSize: 11 } },
    },
    MuiCard: { defaultProps: { variant: "outlined" } },
    MuiCssBaseline: {
      styleOverrides: {
        body: { overscrollBehavior: "none" },
        a: { color: "inherit", textDecoration: "none" },
        ":focus-visible": { outlineOffset: 3 },
      },
    },
  },
});

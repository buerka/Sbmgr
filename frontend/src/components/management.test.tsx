import { Provider } from "react-redux";
import { MemoryRouter } from "react-router-dom";
import { ThemeProvider } from "@mui/material";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { theme } from "../theme";
import { api } from "../api";
import {
  createAdminStore,
  loadCatalog,
  openAction,
  refreshSnapshot,
  signedIn,
} from "../store";
import { fixtureSnapshot, fixtureUser, quotaAction } from "../test-fixture";
import { ActionMenu } from "./common";
import { ActionDialog } from "./ActionDialog";
import { Users } from "../pages/Users";

function setup(ui: ReactNode) {
  const store = createAdminStore();
  store.dispatch(signedIn({ csrf: "test-only" }));
  store.dispatch(refreshSnapshot.fulfilled(fixtureSnapshot(), "fixture"));
  store.dispatch(loadCatalog.fulfilled([quotaAction], "fixture"));
  render(
    <Provider store={store}>
      <ThemeProvider theme={theme}>
        <MemoryRouter>{ui}</MemoryRouter>
      </ThemeProvider>
    </Provider>,
  );
  return store;
}
afterEach(() => vi.restoreAllMocks());
it("MUI menu supports keyboard dismissal and opens the selected action dialog", async () => {
  const user = userEvent.setup();
  setup(
    <>
      <ActionMenu
        label="用户设置"
        items={[{ id: "user.set", context: { user: "alice" } }]}
      />
      <ActionDialog />
    </>,
  );
  const trigger = screen.getByRole("button", { name: "用户设置" });
  await user.click(trigger);
  expect(screen.getByRole("menu", { name: "用户设置" })).toBeVisible();
  await user.keyboard("{Escape}");
  await waitFor(() =>
    expect(screen.queryByRole("menu")).not.toBeInTheDocument(),
  );
  expect(trigger).toHaveFocus();
  await user.click(trigger);
  await user.click(screen.getByRole("menuitem", { name: "配额与限速" }));
  expect(screen.getByRole("dialog", { name: "配额与限速" })).toBeVisible();
  expect(screen.getByLabelText("配额")).toHaveValue("100G");
  await user.click(screen.getByRole("button", { name: "取消" }));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});
it("quota form submits only the intended edit and clears the dialog after acceptance", async () => {
  const submit = vi.spyOn(api, "action").mockResolvedValue({
    id: "fixture-job",
    title: "配额与限速",
    status: "running",
    message: "",
  });
  const store = setup(<ActionDialog />);
  store.dispatch(openAction({ id: "user.set", context: { user: "alice" } }));
  const user = userEvent.setup();
  await user.clear(await screen.findByLabelText("配额"));
  await user.type(screen.getByLabelText("配额"), "120G");
  await user.click(screen.getByRole("button", { name: "保存修改" }));
  await waitFor(() =>
    expect(submit).toHaveBeenCalledWith({
      action: "user.set",
      fields: { user: "alice", quota: "120G" },
      confirm: false,
    }),
  );
  expect(store.getState().admin.dialog).toBeNull();
  expect(store.getState().admin.job?.id).toBe("fixture-job");
});
it("background snapshots preserve a typed user search and its results", async () => {
  const store = setup(<Users />),
    user = userEvent.setup();
  const next = fixtureSnapshot();
  next.users.push(fixtureUser("bob"));
  store.dispatch(refreshSnapshot.fulfilled(next, "fixture"));
  const search = screen.getByRole("textbox", { name: "搜索用户" });
  await user.type(search, "bob");
  expect(
    within(screen.getByRole("table")).getByRole("link", { name: "bob" }),
  ).toBeVisible();
  expect(screen.queryByRole("link", { name: "alice" })).not.toBeInTheDocument();
  store.dispatch(
    refreshSnapshot.fulfilled(
      { ...next, time: "2026-01-01T00:00:10Z" },
      "fixture",
    ),
  );
  expect(search).toHaveValue("bob");
  expect(search).toHaveFocus();
});

import { Provider } from "react-redux";
import { MemoryRouter } from "react-router-dom";
import { ThemeProvider } from "../theme";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { api } from "../api";
import {
  createAdminStore,
  jobReceived,
  loadCatalog,
  openAction,
  refreshSnapshot,
  signedIn,
} from "../store";
import { fixtureSnapshot, fixtureUser, quotaAction } from "../test-fixture";
import { ActionMenu } from "./common";
import { ActionDialog } from "./ActionDialog";
import { Users } from "../pages/Users";
import { App } from "../App";

function setup(ui: ReactNode) {
  const store = createAdminStore();
  store.dispatch(signedIn({ csrf: "test-only" }));
  store.dispatch(refreshSnapshot.fulfilled(fixtureSnapshot(), "fixture"));
  store.dispatch(loadCatalog.fulfilled([quotaAction], "fixture"));
  render(
    <Provider store={store}>
      <ThemeProvider>
        <MemoryRouter>{ui}</MemoryRouter>
      </ThemeProvider>
    </Provider>,
  );
  return store;
}
afterEach(() => vi.restoreAllMocks());
it("does not emit Redux state diagnostics while the initial snapshot is loading", () => {
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  const warning = vi.spyOn(console, "warn").mockImplementation(() => {});
  const store = createAdminStore();
  store.dispatch(signedIn({ csrf: "test-only" }));
  render(
    <Provider store={store}>
      <ThemeProvider>
        <MemoryRouter>
          <App />
        </MemoryRouter>
      </ThemeProvider>
    </Provider>,
  );
  expect(screen.getByText("正在读取管理状态…")).toBeVisible();
  expect(warning).not.toHaveBeenCalled();
});
it("filters, sorts, hides columns and paginates without changing user state", async () => {
  const store = setup(<Users />),
    user = userEvent.setup();
  const next = fixtureSnapshot();
  next.users = Array.from({ length: 12 }, (_, i) =>
    fixtureUser(`user-${i + 1}`),
  );
  next.users[4].status = "已禁用";
  next.users[4].enabled = false;
  store.dispatch(refreshSnapshot.fulfilled(next, "fixture"));
  await user.click(screen.getByRole("button", { name: "下一页" }));
  expect(screen.getByRole("link", { name: "user-11" })).toBeVisible();
  await user.click(screen.getByRole("button", { name: "用户状态筛选" }));
  await user.click(screen.getByRole("menuitemradio", { name: /需关注/ }));
  expect(screen.getByRole("link", { name: "user-5" })).toBeVisible();
  expect(screen.queryByRole("link", { name: "user-11" })).toBeNull();
  await user.click(screen.getByRole("button", { name: "重置" }));
  await user.click(screen.getByRole("button", { name: "按用户名降序排列" }));
  expect(
    within(screen.getByRole("table")).getAllByRole("link")[0],
  ).toHaveTextContent("user-12");
  await user.click(screen.getByRole("button", { name: "显示列" }));
  await user.click(screen.getByRole("menuitemcheckbox", { name: "实时速率" }));
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("columnheader", { name: "实时速率" })).toBeNull();
  expect(store.getState().admin.snapshot).toEqual(next);
});
it("switches theme and finds users from the navigation search", async () => {
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  vi.spyOn(api, "snapshot").mockResolvedValue(fixtureSnapshot());
  const user = userEvent.setup();
  setup(<App />);
  await user.click(screen.getByRole("button", { name: "切换主题" }));
  await user.click(screen.getByRole("menuitemradio", { name: "浅色" }));
  expect(document.documentElement).not.toHaveClass("dark");
  await user.click(screen.getByRole("button", { name: "切换主题" }));
  await user.click(screen.getByRole("menuitemradio", { name: "深色" }));
  expect(document.documentElement).toHaveClass("dark");
  await user.keyboard("{Control>}k{/Control}");
  await user.type(
    screen.getByRole("textbox", { name: "搜索页面与用户" }),
    "alice",
  );
  await user.click(screen.getByRole("button", { name: "打开用户：alice" }));
  expect(screen.getByRole("heading", { name: "alice" })).toBeVisible();
  expect(screen.queryByRole("dialog")).toBeNull();
  localStorage.removeItem("sbmgr-theme");
});
it("mobile navigation closes after choosing a page and exposes an accessible search trigger", async () => {
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  vi.spyOn(api, "snapshot").mockResolvedValue(fixtureSnapshot());
  const user = userEvent.setup();
  setup(<App />);
  await user.click(screen.getByRole("button", { name: "展开导航" }));
  const navigation = screen.getByRole("dialog", { name: "导航" });
  await user.click(within(navigation).getByRole("link", { name: "线路管理" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByRole("heading", { name: "线路管理" })).toBeVisible();
  await user.click(screen.getByRole("button", { name: "搜索页面与用户" }));
  expect(screen.getByRole("textbox", { name: "搜索页面与用户" })).toBeVisible();
});
it("Shadcn menu supports keyboard dismissal and opens the selected action dialog", async () => {
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
  await waitFor(() => expect(trigger).toHaveFocus());
});
it("quota form submits only the intended edit and closes after confirmed completion", async () => {
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
  expect(screen.getByRole("dialog")).toBeVisible();
  expect(store.getState().admin.job?.id).toBe("fixture-job");
  store.dispatch(
    jobReceived({
      id: "fixture-job",
      title: "配额与限速",
      status: "success",
      message: "已保存",
    }),
  );
  await waitFor(() => expect(store.getState().admin.dialog).toBeNull());
});
it("a rejected background job keeps values available for correction", async () => {
  vi.spyOn(api, "action").mockResolvedValue({
    id: "failed-job",
    title: "配额与限速",
    status: "running",
    message: "",
  });
  const store = setup(<ActionDialog />),
    user = userEvent.setup();
  store.dispatch(openAction({ id: "user.set", context: { user: "alice" } }));
  await user.clear(await screen.findByLabelText("配额"));
  await user.type(screen.getByLabelText("配额"), "invalid");
  await user.click(screen.getByRole("button", { name: "保存修改" }));
  await waitFor(() =>
    expect(store.getState().admin.job?.id).toBe("failed-job"),
  );
  store.dispatch(
    jobReceived({
      id: "failed-job",
      title: "配额与限速",
      status: "failed",
      message: "配额格式无效",
    }),
  );
  expect(await screen.findByText("配额格式无效")).toBeVisible();
  expect(screen.getByLabelText("配额")).toHaveValue("invalid");
  expect(screen.getByRole("button", { name: "保存修改" })).toBeEnabled();
});
it("preserves unsaved edits and original values through a failed save and background refresh", async () => {
  const submit = vi
    .spyOn(api, "action")
    .mockRejectedValue(new Error("连接失败，请重试"));
  const store = setup(<ActionDialog />),
    user = userEvent.setup();
  store.dispatch(openAction({ id: "user.set", context: { user: "alice" } }));
  const quota = await screen.findByLabelText("配额");
  expect(screen.getByRole("button", { name: "保存修改" })).toBeDisabled();
  await user.clear(quota);
  await user.type(quota, "130G");
  expect(screen.getByText("原值：100G")).toBeVisible();
  const next = fixtureSnapshot();
  next.users[0].quota = 110 * 2 ** 30;
  store.dispatch(refreshSnapshot.fulfilled(next, "refresh"));
  expect(quota).toHaveValue("130G");
  await user.click(screen.getByRole("button", { name: "保存修改" }));
  expect(await screen.findByText("连接失败，请重试")).toBeVisible();
  expect(quota).toHaveValue("130G");
  expect(submit).toHaveBeenCalledOnce();
  await user.click(screen.getByRole("button", { name: "取消" }));
  expect(screen.getByRole("dialog")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "放弃修改" }));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});
it("selecting another user reloads their existing settings", async () => {
  const store = setup(<ActionDialog />),
    user = userEvent.setup();
  const next = fixtureSnapshot(),
    bob = fixtureUser("bob");
  bob.quota = 50 * 2 ** 30;
  next.users.push(bob);
  store.dispatch(refreshSnapshot.fulfilled(next, "fixture"));
  store.dispatch(openAction({ id: "user.set", context: {} }));
  await user.click(await screen.findByRole("combobox", { name: /用户/ }));
  await user.click(screen.getByRole("option", { name: "alice" }));
  expect(screen.getByLabelText("配额")).toHaveValue("100G");
  await user.click(screen.getByRole("combobox", { name: /用户/ }));
  await user.click(screen.getByRole("option", { name: "bob" }));
  expect(screen.getByLabelText("配额")).toHaveValue("50G");
  expect(screen.getByRole("button", { name: "保存修改" })).toBeDisabled();
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

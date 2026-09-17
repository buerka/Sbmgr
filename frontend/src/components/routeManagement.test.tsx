import { Provider } from "react-redux";
import { MemoryRouter } from "react-router-dom";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { Snapshot } from "../types";
import { api } from "../api";
import {
  createAdminStore,
  jobReceived,
  loadCatalog,
  openAction,
  refreshSnapshot,
  signedIn,
} from "../store";
import { fixtureSnapshot } from "../test-fixture";
import { ActionDialog } from "./ActionDialog";
import { Users } from "../pages/Users";
import { RouteCanvas } from "./RouteCanvas";
import { Account } from "../pages/Account";

function topology(): Snapshot {
  const s = fixtureSnapshot();
  s.role = "master";
  s.revision = 7;
  s.members = [
    { id: "west", master: true, host: "" },
    {
      id: "east",
      master: false,
      host: "relay.example",
      client: { server: "relay.example", port: 443 },
    },
  ];
  s.routes = [
    {
      id: "west-direct",
      name: "West",
      entry: "west",
      hops: ["west"],
      exit: "",
    },
    {
      id: "west-home",
      name: "Home via West",
      entry: "west",
      hops: ["west"],
      exit: "home",
    },
    {
      id: "east-direct",
      name: "East",
      entry: "east",
      hops: ["east"],
      exit: "",
    },
  ];
  s.outbounds = s.routes.map((r) => ({
    name: r.name!,
    outbound: `sbmgr-mesh-${r.id}`,
  }));
  s.users[0].devices[0].assignment_version = "original-version";
  s.users[0].nodes = [
    {
      name: "My custom node",
      device: "phone",
      outbound: "sbmgr-mesh-west-direct",
      entry: "west",
      upload: 23,
      download: 12,
      up_mbps: 15,
      down_mbps: 50,
      current_up: 0,
      current_down: 0,
    },
  ];
  return s;
}
function setup(ui: ReactNode, snapshot = topology()) {
  const store = createAdminStore();
  store.dispatch(signedIn({ csrf: "test-only", username: "fixture-admin" }));
  store.dispatch(refreshSnapshot.fulfilled(snapshot, "fixture"));
  store.dispatch(
    loadCatalog.fulfilled(
      [
        {
          id: "node.assign",
          title: "分配线路",
          scope: "user",
          effect: "保存后应用",
          danger: false,
          fields: [],
        },
      ],
      "fixture",
    ),
  );
  render(
    <Provider store={store}>
      <MemoryRouter>{ui}</MemoryRouter>
    </Provider>,
  );
  return store;
}
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("opens assignment directly from the user list, prefills grants and sends one atomic selection", async () => {
  const submit = vi.spyOn(api, "action").mockResolvedValue({
    id: "assign-job",
    title: "分配线路",
    status: "running",
    message: "",
  });
  const store = setup(
      <>
        <Users />
        <ActionDialog />
      </>,
    ),
    user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "分配线路：alice" }));
  expect(screen.getByRole("checkbox", { name: "West" })).toBeChecked();
  expect(screen.getByRole("button", { name: "保存线路分配" })).toBeDisabled();
  await user.click(screen.getByRole("checkbox", { name: "Home via West" }));
  const refreshed = topology();
  refreshed.users[0].devices[0].assignment_version = "concurrent-change";
  act(() => store.dispatch(refreshSnapshot.fulfilled(refreshed, "refresh")));
  await user.click(screen.getByRole("button", { name: "保存线路分配" }));
  await waitFor(() => expect(submit).toHaveBeenCalledOnce());
  const input = submit.mock.calls[0][0];
  expect(input.fields.expected).toBe("original-version");
  expect(
    JSON.parse(input.fields.selection).map(
      (r: { outbound: string }) => r.outbound,
    ),
  ).toEqual(["sbmgr-mesh-west-direct", "sbmgr-mesh-west-home"]);
  act(() =>
    store.dispatch(
      jobReceived({
        id: "assign-job",
        title: "分配线路",
        status: "failed",
        message: "此设备的线路已被修改",
      }),
    ),
  );
  expect(await screen.findByText("此设备的线路已被修改")).toBeVisible();
  expect(screen.getByRole("checkbox", { name: "Home via West" })).toBeChecked();
  await user.click(screen.getByRole("button", { name: "取消" }));
  expect(screen.getByRole("dialog")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "放弃修改" }));
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("keeps existing grants editable while blocking new assignments of unapplied topology", async () => {
  const snapshot = topology();
  snapshot.mesh_pending = true;
  const store = setup(<ActionDialog />, snapshot);
  act(() =>
    store.dispatch(
      openAction({ id: "node.assign", context: { user: "alice" } }),
    ),
  );
  expect(screen.getByRole("checkbox", { name: "West" })).toBeEnabled();
  expect(
    screen.getByRole("checkbox", { name: "Home via West" }),
  ).toBeDisabled();
  const user = userEvent.setup();
  await user.click(screen.getByRole("checkbox", { name: "West" }));
  expect(screen.getByRole("button", { name: "保存线路分配" })).toBeDisabled();
  expect(screen.getByText(/将撤销：West/)).toBeVisible();
});

it("connects by keyboard, saves a real route action, preserves failed drafts and isolates entry exits", async () => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.spyOn(api, "routeInventory").mockImplementation(async (member) => ({
    member,
    exits: [
      { tag: "", name: "直接出站", type: "direct" },
      {
        tag: member === "west" ? "new-exit" : "east-only",
        name: member === "west" ? "New exit" : "East only",
        type: "socks",
      },
    ],
  }));
  const submit = vi.spyOn(api, "action").mockResolvedValue({
    id: "wire-job",
    title: "保存线路",
    status: "running",
    message: "",
  });
  const store = setup(<RouteCanvas />),
    user = userEvent.setup();
  await screen.findByRole("button", { name: "落地：New exit" });
  await user.click(screen.getByRole("button", { name: "新建线路" }));
  screen.getByRole("button", { name: "从 West 连线" }).focus();
  await user.keyboard("{Enter}");
  await user.click(screen.getByRole("button", { name: "落地：New exit" }));
  expect(screen.getByLabelText("线路标识")).toHaveValue("west-new-exit");
  await user.click(screen.getByRole("button", { name: "保存线路" }));
  expect(submit).toHaveBeenCalledWith({
    action: "mesh.wire",
    confirm: false,
    fields: {
      id: "west-new-exit",
      entry: "west",
      exit: "new-exit",
      revision: "7",
      replace: "false",
    },
  });
  act(() =>
    store.dispatch(
      jobReceived({
        id: "wire-job",
        title: "保存线路",
        status: "failed",
        message: "保存失败",
      }),
    ),
  );
  expect(await screen.findByText("保存失败")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "入口：East" }));
  expect(screen.getByText("当前连线尚未保存。")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "放弃修改" }));
  expect(
    await screen.findByRole("button", { name: "落地：East only" }),
  ).toBeVisible();
  expect(screen.queryByRole("button", { name: "落地：New exit" })).toBeNull();
});

it("prefills administrator name, rejects password mismatch, and logs out only after save succeeds", async () => {
  const save = vi
    .spyOn(api, "account")
    .mockRejectedValueOnce(new Error("当前密码不正确"))
    .mockResolvedValue({ message: "请重新登录" });
  const store = setup(<Account />),
    user = userEvent.setup();
  expect(screen.getByLabelText("用户名")).toHaveValue("fixture-admin");
  await user.type(screen.getByLabelText("当前密码"), "test-current-only");
  await user.type(screen.getByLabelText("新密码"), "test-password-only");
  await user.type(screen.getByLabelText("确认新密码"), "does-not-match");
  await user.click(screen.getByRole("button", { name: "保存并重新登录" }));
  expect(screen.getByText("两次输入的新密码不一致。")).toBeVisible();
  expect(save).not.toHaveBeenCalled();
  await user.clear(screen.getByLabelText("确认新密码"));
  await user.type(screen.getByLabelText("确认新密码"), "test-password-only");
  await user.click(screen.getByRole("button", { name: "保存并重新登录" }));
  expect(await screen.findByText("当前密码不正确")).toBeVisible();
  expect(store.getState().admin.status).toBe("authenticated");
  await user.click(screen.getByRole("button", { name: "保存并重新登录" }));
  await waitFor(() => expect(store.getState().admin.status).toBe("anonymous"));
  expect(JSON.stringify(store.getState())).not.toContain("test-password-only");
  expect(store.getState().admin.notice?.message).toBe("请重新登录");
});

import axios from "axios";
import { deliveryFilename } from "./downloadFilename";
import type {
  Action,
  ActionInput,
  Context,
  Job,
  Session,
  Snapshot,
  RouteInventory,
  PortalSnapshot,
  AnalyticsSnapshot,
  SiteBlocksSnapshot,
  MachineTrafficHistory,
} from "./types";

const http = axios.create({
  baseURL: new URL("./api", window.location.href).pathname,
  withCredentials: true,
  timeout: 20000,
});
let csrf = () => "";
let unauthenticated = () => {};
let credentialPolling = 0;
export function configureAPI(options: {
  csrf: () => string;
  unauthenticated: () => void;
}) {
  csrf = options.csrf;
  unauthenticated = options.unauthenticated;
}
http.interceptors.request.use((config) => {
  config.headers.set("X-CSRF-Token", csrf());
  return config;
});
async function request<T>(
  path: string,
  data?: unknown,
  signal?: AbortSignal,
  requestID?: string,
): Promise<T> {
  try {
    const response = await http.request<T>({
      url: path,
      method: data === undefined ? "GET" : "POST",
      data,
      signal,
      headers: requestID ? { "X-Request-ID": requestID } : undefined,
    });
    return response.data;
  } catch (error) {
    if (axios.isAxiosError(error)) {
      if (
        error.response?.status === 401 &&
        path !== "/login" &&
        credentialPolling === 0
      )
        unauthenticated();
      const message = error.response?.data?.error;
      throw new APIError(
        typeof message === "string"
          ? message
          : "无法连接管理服务，请稍后重试。",
        error.response?.status,
      );
    }
    throw new Error("请求未完成，请刷新状态后重试。");
  }
}
class APIError extends Error {
  constructor(
    message: string,
    readonly status?: number,
  ) {
    super(message);
  }
}
export type TaskProgress = (message: string) => void;
export class TaskPendingError extends Error {}
const volatileTasks = new Map<string, string>();
const taskPrefix = `sbmgr-task:${http.defaults.baseURL}:`;
function pendingTask(key: string): string | null {
  try {
    return (
      sessionStorage.getItem(taskPrefix + key) || volatileTasks.get(key) || null
    );
  } catch {
    return volatileTasks.get(key) || null;
  }
}
function rememberTask(key: string, id: string) {
  volatileTasks.set(key, id);
  try {
    sessionStorage.setItem(taskPrefix + key, id);
  } catch {
    /* Memory remains in the running request. */
  }
}
function forgetTask(key: string) {
  volatileTasks.delete(key);
  try {
    sessionStorage.removeItem(taskPrefix + key);
  } catch {
    /* Storage may be unavailable. */
  }
}
async function readTask(id: string, signal?: AbortSignal) {
  let job: Job;
  try {
    job = await request<Job>(
      `/jobs/${encodeURIComponent(id)}`,
      undefined,
      signal,
    );
  } catch (error) {
    if (error instanceof APIError && error.status === 404) {
      return {
        id,
        title: "上次管理操作",
        status: "unknown",
        message:
          "任务记录不存在或已过期，无法确认是否已保存。请先核对当前设置。",
      } as Job;
    }
    throw error;
  }
  if (
    (job.status === "success" || job.status === "failed") &&
    pendingTask("action") === id
  )
    forgetTask("action");
  return job;
}
async function submitTask(
  path: string,
  data: unknown,
  key: string,
): Promise<Job> {
  const previous = pendingTask(key);
  if (previous) {
    const prior = await readTask(previous).catch(() => null);
    if (
      key === "action" &&
      prior &&
      (prior.status === "success" || prior.status === "failed")
    ) {
      forgetTask(key);
      throw new Error(
        "上次操作已结束，本次修改尚未提交。请核对当前设置后再次保存。",
      );
    }
    throw new TaskPendingError(
      "上次操作的结果待确认，本次修改尚未提交。请查询上次结果，或刷新页面恢复任务。",
    );
  }
  const id = Array.from(crypto.getRandomValues(new Uint8Array(32)), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
  rememberTask(key, id);
  try {
    return await request<Job>(path, data, undefined, id);
  } catch (error) {
    if (error instanceof APIError && error.status && error.status < 500) {
      forgetTask(key);
      throw error;
    }
    // Submission is never automatically repeated. Query the same receipt if
    // an acknowledgement was lost while the server accepted the operation.
    try {
      return await readTask(id);
    } catch {
      throw new TaskPendingError(
        "提交结果待确认。任务编号已保留，请查询上次结果，不要重复提交。 ",
      );
    }
  }
}
async function finishTask(
  job: Job,
  key: string,
  progress?: TaskProgress,
): Promise<Job> {
  const deadline = Date.now() + 6 * 60_000;
  while (job.status === "running" && Date.now() < deadline) {
    progress?.(job.message || "正在执行；无需重复提交。 ");
    await new Promise((resolve) => setTimeout(resolve, 1000));
    try {
      job = await readTask(job.id);
    } catch (error) {
      if (
        error instanceof APIError &&
        (error.status === 401 || error.status === 404)
      ) {
        throw new TaskPendingError(
          "无法确认上次任务结果。记录可能已过期或服务已重启，请先检查当前设置。 ",
        );
      }
      progress?.("暂时无法确认结果，正在重新连接；任务不会重复提交。 ");
      await new Promise((resolve) => setTimeout(resolve, 1500));
    }
  }
  if (job.status === "running")
    throw new TaskPendingError("操作仍在处理中或连接已中断，请查询上次结果。 ");
  if (job.status === "unknown") throw new TaskPendingError(job.message);
  forgetTask(key);
  if (job.status !== "success") throw new Error(job.message);
  return job;
}
async function credentialTask(
  path: string,
  data: unknown,
  key: string,
  progress?: TaskProgress,
) {
  credentialPolling++;
  try {
    progress?.("正在提交…");
    const job = await submitTask(path, data, key);
    data = undefined;
    return await finishTask(job, key, progress);
  } finally {
    credentialPolling--;
  }
}
async function resumeCredential(key: string, progress?: TaskProgress) {
  credentialPolling++;
  try {
    const id = pendingTask(key);
    if (!id) throw new Error("没有待确认的任务。 ");
    let job: Job;
    try {
      job = await readTask(id);
    } catch {
      throw new TaskPendingError(
        "暂时无法确认结果，请稍后查询；服务重启后请先检查当前设置。 ",
      );
    }
    return await finishTask(job, key, progress);
  } finally {
    credentialPolling--;
  }
}
async function deliveryFile(context: Context, format: "link" | "yaml") {
  try {
    const response = await http.post<Blob>(
      "/delivery",
      { ...context, format },
      { responseType: "blob" },
    );
    const header = response.headers["content-disposition"];
    return {
      blob: response.data,
      filename: deliveryFilename(
        typeof header === "string" ? header : null,
        format,
      ),
    };
  } catch (error) {
    if (axios.isAxiosError(error) && error.response?.status === 401)
      unauthenticated();
    throw new Error("订阅交付失败，请检查设备授权、配额及有效期。");
  }
}
async function subscriptionLink(context: Context) {
  const text = (await (await deliveryFile(context, "link")).blob.text()).trim();
  try {
    const url = new URL(text);
    if (!/^https?:$/.test(url.protocol) || /\s/.test(text)) throw new Error();
    return text;
  } catch {
    throw new Error("订阅地址格式不正确，请检查订阅服务设置。");
  }
}
export const api = {
  portalInvite: (user: string) =>
    request<{ url: string; expires: string; message: string }>(
      "/portal-invite",
      { user },
    ),
  inviteInfo: (token: string) =>
    request<{ username: string; expires: string }>("/invite/info", { token }),
  acceptInvite: (token: string, password: string) =>
    request<{ message: string }>("/invite/accept", { token, password }),
  me: () => request<PortalSnapshot>("/me"),
  siteBlocks: () => request<SiteBlocksSnapshot>("/me/site-blocks"),
  updateSiteBlocks: (input: {
    action: "add" | "remove";
    domain: string;
    expected: string;
  }) => request<SiteBlocksSnapshot>("/me/site-blocks", input),
  analytics: (
    input: {
      user?: string;
      device?: string;
      days: 1 | 7 | 30;
      page?: number;
      search?: string;
      sort?: "traffic" | "connections";
    },
    signal?: AbortSignal,
  ) => request<AnalyticsSnapshot>("/analytics", input, signal),
  machineTrafficHistory: (member: string, page: number, signal?: AbortSignal) =>
    request<MachineTrafficHistory>(
      "/machine-traffic/history",
      { member, page },
      signal,
    ),
  selfDevice: (input: {
    action: "add" | "rename" | "delete" | "rotate-link";
    device?: string;
    name?: string;
    from?: string;
    expected: string;
  }) => request<{ message: string; pending: boolean }>("/me/devices", input),
  selfPassword: (
    current_password: string,
    new_password: string,
    progress?: TaskProgress,
  ) =>
    credentialTask(
      "/me/password",
      { current_password, new_password },
      "self",
      progress,
    ),
  portalAccount: (
    user: string,
    enabled: boolean,
    password: string,
    progress?: TaskProgress,
  ) =>
    credentialTask(
      "/portal-account",
      { user, enabled, password },
      `portal:${user}`,
      progress,
    ),
  resumeCredential: (key: string, progress?: TaskProgress) =>
    resumeCredential(key, progress),
  pendingCredential: (key: string) => !!pendingTask(key),
  clearPendingCredential: (key: string) => forgetTask(key),
  session: () => request<Session>("/session"),
  login: (username: string, password: string) =>
    request<Session>("/login", { username, password }),
  logout: () => request("/logout", {}),
  account: (
    username: string,
    current_password: string,
    new_password: string,
    progress?: TaskProgress,
  ) =>
    credentialTask(
      "/account",
      { username, current_password, new_password },
      "admin",
      progress,
    ),
  routeInventory: (member: string) =>
    request<RouteInventory>(`/route-inventory/${encodeURIComponent(member)}`),
  snapshot: () => request<Snapshot>("/state"),
  catalog: () => request<Action[]>("/catalog"),
  action: (input: ActionInput) => submitTask("/actions", input, "action"),
  clearPendingAction: () => forgetTask("action"),
  resumeAction: async () => {
    const id = pendingTask("action");
    return id ? readTask(id) : null;
  },
  job: (id: string, signal?: AbortSignal) => readTask(id, signal),
  async copySubscriptionLink(context: Context) {
    if (!navigator.clipboard) {
      throw new Error(
        "当前浏览器无法使用剪贴板，请通过 HTTPS 访问或选择下载 TXT。",
      );
    }
    try {
      if (typeof ClipboardItem !== "undefined" && navigator.clipboard.write) {
        // Start the clipboard operation inside the user's gesture, before the network response.
        const content = subscriptionLink(context).then(
          (text) => new Blob([text], { type: "text/plain" }),
        );
        void content.catch(() => {});
        await navigator.clipboard.write([
          new ClipboardItem({ "text/plain": content }),
        ]);
      } else {
        await navigator.clipboard.writeText(await subscriptionLink(context));
      }
    } catch {
      throw new Error(
        "未能复制链接，请检查设备授权及剪贴板权限，或选择下载 TXT。",
      );
    }
  },
  async delivery(context: Context, format: "link" | "yaml") {
    const { blob, filename } = await deliveryFile(context, format);
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = filename;
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  },
};

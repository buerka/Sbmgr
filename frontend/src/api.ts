import axios from "axios";
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
} from "./types";

const http = axios.create({
  baseURL: new URL("./api", window.location.href).pathname,
  withCredentials: true,
  timeout: 20000,
});
let csrf = () => "";
let unauthenticated = () => {};
export function configureAPI(options: {
  csrf: () => string;
  unauthenticated: () => void;
}) {
  csrf = options.csrf;
  unauthenticated = options.unauthenticated;
}
http.interceptors.request.use((config) => {
  if (config.method?.toLowerCase() === "post")
    config.headers.set("X-CSRF-Token", csrf());
  return config;
});
async function request<T>(
  path: string,
  data?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  try {
    const response = await http.request<T>({
      url: path,
      method: data === undefined ? "GET" : "POST",
      data,
      signal,
    });
    return response.data;
  } catch (error) {
    if (axios.isAxiosError(error)) {
      if (error.response?.status === 401 && path !== "/login")
        unauthenticated();
      const message = error.response?.data?.error;
      throw new Error(
        typeof message === "string"
          ? message
          : "无法连接管理服务，请稍后重试。",
      );
    }
    throw new Error("请求未完成，请刷新状态后重试。");
  }
}
async function deliveryFile(context: Context, format: "link" | "yaml") {
  try {
    const response = await http.post<Blob>(
      "/delivery",
      { ...context, format },
      { responseType: "blob" },
    );
    return response.data;
  } catch (error) {
    if (axios.isAxiosError(error) && error.response?.status === 401)
      unauthenticated();
    throw new Error("订阅交付失败，请检查设备授权、配额及有效期。");
  }
}
async function subscriptionLink(context: Context) {
  const text = (await (await deliveryFile(context, "link")).text()).trim();
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
  selfDevice: (input: {
    action: "add" | "rename" | "delete" | "rotate-link";
    device?: string;
    name?: string;
    from?: string;
    expected: string;
  }) => request<{ message: string; pending: boolean }>("/me/devices", input),
  selfPassword: (current_password: string, new_password: string) =>
    request<{ message: string }>("/me/password", {
      current_password,
      new_password,
    }),
  portalAccount: (user: string, enabled: boolean, password: string) =>
    request<{ message: string }>("/portal-account", {
      user,
      enabled,
      password,
    }),
  session: () => request<Session>("/session"),
  login: (username: string, password: string) =>
    request<Session>("/login", { username, password }),
  logout: () => request("/logout", {}),
  account: (username: string, current_password: string, new_password: string) =>
    request<{ message: string }>("/account", {
      username,
      current_password,
      new_password,
    }),
  routeInventory: (member: string) =>
    request<RouteInventory>(`/route-inventory/${encodeURIComponent(member)}`),
  snapshot: () => request<Snapshot>("/state"),
  catalog: () => request<Action[]>("/catalog"),
  action: (input: ActionInput) => request<Job>("/actions", input),
  job: (id: string, signal?: AbortSignal) =>
    request<Job>(`/jobs/${encodeURIComponent(id)}`, undefined, signal),
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
    const data = await deliveryFile(context, format);
    const url = URL.createObjectURL(data);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download =
      format === "yaml" ? "subscription.yaml" : "subscription.txt";
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  },
};

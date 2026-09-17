import axios from "axios";
import type {
  Action,
  ActionInput,
  Context,
  Job,
  Session,
  Snapshot,
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
export const api = {
  session: () => request<Session>("/session"),
  login: (username: string, password: string) =>
    request<Session>("/login", { username, password }),
  logout: () => request("/logout", {}),
  snapshot: () => request<Snapshot>("/state"),
  catalog: () => request<Action[]>("/catalog"),
  action: (input: ActionInput) => request<Job>("/actions", input),
  job: (id: string, signal?: AbortSignal) =>
    request<Job>(`/jobs/${encodeURIComponent(id)}`, undefined, signal),
  async delivery(context: Context, format: string) {
    try {
      const response = await http.post<Blob>(
        "/delivery",
        { ...context, format },
        { responseType: "blob" },
      );
      const url = URL.createObjectURL(response.data);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download =
        format === "yaml" ? "subscription.yaml" : "subscription.txt";
      anchor.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (error) {
      if (axios.isAxiosError(error) && error.response?.status === 401)
        unauthenticated();
      throw new Error("订阅交付失败，请检查设备授权、配额及有效期。");
    }
  },
};

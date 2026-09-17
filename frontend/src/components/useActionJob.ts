import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { jobReceived, useAppDispatch, useAppSelector } from "../store";
import type { ActionInput } from "../types";

export function useActionJob(onSuccess: () => void) {
  const dispatch = useAppDispatch();
  const job = useAppSelector((s) => s.admin.job);
  const [id, setID] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState("");
  const callback = useRef(onSuccess);
  callback.current = onSuccess;
  useEffect(() => {
    if (!id || job?.id !== id || job.status === "running") return;
    setID("");
    if (job.status === "success") callback.current();
    else setError(job.message || "保存失败，修改仍保留在这里。");
  }, [id, job]);
  async function submit(input: ActionInput) {
    if (sending || id || job?.status === "running") return;
    setSending(true);
    setError("");
    try {
      const result = await api.action(input);
      setID(result.id);
      dispatch(jobReceived(result));
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存失败，请重试。");
    } finally {
      setSending(false);
    }
  }
  return {
    submit,
    error,
    setError,
    busy: sending || !!id,
    blocked: job?.status === "running",
  };
}

import { useState, type FormEvent } from "react";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  IconButton,
  MenuItem,
  TextField,
  Typography,
} from "@mui/material";
import { api } from "../api";
import {
  closeAction,
  jobReceived,
  useAppDispatch,
  useAppSelector,
} from "../store";
import type { Action, Context, Snapshot } from "../types";
import {
  collectFields,
  fieldOptions,
  initialFields,
  lockedField,
} from "./actionFields";
import { Icon } from "./Icons";

export function ActionDialog() {
  const { dialog, catalog, snapshot } = useAppSelector((s) => s.admin),
    dispatch = useAppDispatch();
  const action = catalog.find((a) => a.id === dialog?.id);
  if (!dialog || !action || !snapshot) return null;
  return (
    <ActionForm
      key={JSON.stringify(dialog)}
      action={action}
      context={dialog.context}
      snapshot={snapshot}
      onClose={() => dispatch(closeAction())}
    />
  );
}
export function ActionForm({
  action,
  context,
  snapshot,
  onClose,
}: {
  action: Action;
  context: Context;
  snapshot: Snapshot;
  onClose: () => void;
}) {
  const [initial] = useState(() => initialFields(action, context, snapshot));
  const [values, setValues] = useState<Context>(initial),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const dispatch = useAppDispatch();
  const setField = (key: string, value: string) =>
    setValues((old) => ({
      ...old,
      ...(key === "user" ? { device: "", node: "", from: "" } : {}),
      [key]: value,
    }));
  const close = () => {
    if (!busy) {
      setValues({});
      onClose();
    }
  };
  async function submit(event: FormEvent) {
    event.preventDefault();
    setError("");
    const fields = collectFields(action, values, initial, context);
    const missing = action.fields.find(
      (f) => f.required && !fields[f.key]?.trim(),
    );
    if (missing) {
      setError(`请填写${missing.label}`);
      return;
    }
    setBusy(true);
    try {
      const job = await api.action({
        action: action.id,
        fields,
        confirm: action.danger,
      });
      setValues({});
      dispatch(jobReceived(job));
      onClose();
    } catch (error) {
      setError(error instanceof Error ? error.message : "操作失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog
      open
      onClose={close}
      fullWidth
      maxWidth="sm"
      aria-labelledby="action-title"
    >
      <Box component="form" onSubmit={submit}>
        <DialogTitle
          component="div"
          id="action-heading-container"
          sx={{ pr: 7, pb: 2 }}
        >
          <Typography
            component="span"
            variant="caption"
            color="text.secondary"
            display="block"
            mb={0.5}
          >
            {context.user
              ? `用户 ${context.user}${context.device ? ` / ${context.device}` : ""}`
              : "管理操作"}
          </Typography>
          <Typography id="action-title" component="h2" variant="h6">
            {action.title}
          </Typography>
          <IconButton
            aria-label="关闭"
            onClick={close}
            disabled={busy}
            sx={{ position: "absolute", right: 17, top: 18 }}
          >
            <Icon name="close" />
          </IconButton>
        </DialogTitle>
        <DialogContent>
          <Alert severity={action.danger ? "warning" : "info"} sx={{ mb: 3 }}>
            执行成功后：{action.effect}
          </Alert>
          <Box className="form-grid">
            {action.fields.map((field) => {
              const value = values[field.key] || "",
                disabled = busy || lockedField(field, context);
              if (field.type === "checkbox")
                return (
                  <FormControlLabel
                    key={field.key}
                    className="checkbox-field"
                    control={
                      <Checkbox
                        checked={value === "true"}
                        disabled={disabled}
                        onChange={(e) =>
                          setField(field.key, String(e.target.checked))
                        }
                      />
                    }
                    label={field.label}
                  />
                );
              if (field.type === "multi-select")
                return (
                  <TextField
                    key={field.key}
                    select
                    label={field.label}
                    value={value ? value.split(",") : []}
                    required={field.required}
                    disabled={disabled}
                    onChange={(e) =>
                      setField(
                        field.key,
                        Array.isArray(e.target.value)
                          ? e.target.value.join(",")
                          : e.target.value,
                      )
                    }
                    SelectProps={{ multiple: true }}
                  >
                    {fieldOptions(field, values, snapshot).map(([v, l]) => (
                      <MenuItem key={v} value={v}>
                        {l}
                      </MenuItem>
                    ))}
                  </TextField>
                );
              if (field.type === "select")
                return (
                  <TextField
                    key={field.key}
                    select
                    label={field.label}
                    value={value}
                    required={field.required}
                    disabled={disabled}
                    onChange={(e) => setField(field.key, e.target.value)}
                  >
                    <MenuItem value="">
                      {field.required ? "请选择" : "保持原值 / 默认"}
                    </MenuItem>
                    {fieldOptions(field, values, snapshot).map(([v, l]) => (
                      <MenuItem key={v} value={v}>
                        {l}
                      </MenuItem>
                    ))}
                  </TextField>
                );
              return (
                <TextField
                  key={field.key}
                  className={
                    field.type === "secret-text" ? "full-width" : undefined
                  }
                  label={field.label}
                  value={value}
                  required={field.required}
                  disabled={disabled}
                  onChange={(e) => setField(field.key, e.target.value)}
                  type={
                    field.type === "number"
                      ? "number"
                      : field.type === "date"
                        ? "date"
                        : "text"
                  }
                  multiline={field.type === "secret-text"}
                  minRows={field.type === "secret-text" ? 5 : undefined}
                  autoComplete="off"
                  inputProps={{
                    ...(field.type === "number" ? { min: 0, step: "any" } : {}),
                    ...(field.type === "secret-text"
                      ? { spellCheck: false }
                      : {}),
                    maxLength: 131072,
                  }}
                  InputLabelProps={
                    field.type === "date" ? { shrink: true } : undefined
                  }
                  placeholder={
                    field.type === "secret-text"
                      ? "粘贴单个 JSON 对象，提交后清空"
                      : undefined
                  }
                />
              );
            })}
          </Box>
          {action.fields.length > 0 && (
            <Typography
              variant="caption"
              color="text.secondary"
              display="block"
              mt={2.5}
            >
              新建时可选项留空采用默认值；编辑时留空保持原值。清除列表可填
              -，日期使用“清除到期日”。
            </Typography>
          )}
          {error && (
            <Alert severity="error" sx={{ mt: 2 }}>
              {error}
            </Alert>
          )}
        </DialogContent>
        <DialogActions sx={{ px: 3, pb: 3, gap: 1 }}>
          <Button onClick={close} disabled={busy}>
            取消
          </Button>
          <Button
            type="submit"
            variant="contained"
            color={action.danger ? "warning" : "primary"}
            disabled={busy}
          >
            {busy
              ? "正在提交…"
              : action.danger
                ? "确认执行"
                : action.fields.length
                  ? "保存修改"
                  : "开始执行"}
          </Button>
        </DialogActions>
      </Box>
    </Dialog>
  );
}

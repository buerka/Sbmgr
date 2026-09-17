import {
  configureStore,
  createAsyncThunk,
  createSlice,
  type PayloadAction,
} from "@reduxjs/toolkit";
import {
  useDispatch,
  useSelector,
  type TypedUseSelectorHook,
} from "react-redux";
import { api, configureAPI } from "./api";
import type { Action, ActionTarget, Job, Session, Snapshot } from "./types";

interface AdminState {
  status: "checking" | "anonymous" | "authenticated";
  session: Session | null;
  snapshot: Snapshot | null;
  catalog: Action[];
  loading: boolean;
  error: string;
  dialog: ActionTarget | null;
  job: Job | null;
  notice: { message: string; severity: "success" | "error" | "info" } | null;
}
const initialState: AdminState = {
  status: "checking",
  session: null,
  snapshot: null,
  catalog: [],
  loading: false,
  error: "",
  dialog: null,
  job: null,
  notice: null,
};
export const refreshSnapshot = createAsyncThunk("admin/refresh", () =>
  api.snapshot(),
);
export const loadCatalog = createAsyncThunk("admin/catalog", () =>
  api.catalog(),
);
const slice = createSlice({
  name: "admin",
  initialState,
  reducers: {
    signedIn(state, action: PayloadAction<Session>) {
      state.session = action.payload;
      state.status = "authenticated";
      state.error = "";
    },
    signedOut() {
      return { ...initialState, status: "anonymous" };
    },
    openAction(state, action: PayloadAction<ActionTarget>) {
      if (state.job?.status !== "running") state.dialog = action.payload;
    },
    closeAction(state) {
      state.dialog = null;
    },
    jobReceived(state, action: PayloadAction<Job>) {
      state.job = action.payload;
    },
    notify(state, action: PayloadAction<NonNullable<AdminState["notice"]>>) {
      state.notice = action.payload;
    },
    clearNotice(state) {
      state.notice = null;
    },
  },
  extraReducers: (builder) => {
    builder.addCase(refreshSnapshot.pending, (state) => {
      state.loading = true;
    });
    builder.addCase(refreshSnapshot.fulfilled, (state, action) => {
      state.loading = false;
      if (state.status === "authenticated") {
        state.snapshot = action.payload;
        state.error = "";
      }
    });
    builder.addCase(refreshSnapshot.rejected, (state, action) => {
      state.loading = false;
      state.error = action.error.message || "状态读取失败";
    });
    builder.addCase(loadCatalog.fulfilled, (state, action) => {
      if (state.status === "authenticated") state.catalog = action.payload;
    });
    builder.addCase(loadCatalog.rejected, (state, action) => {
      state.error = action.error.message || "操作列表读取失败";
    });
  },
});
export const {
  signedIn,
  signedOut,
  openAction,
  closeAction,
  jobReceived,
  notify,
  clearNotice,
} = slice.actions;
export const createAdminStore = () =>
  configureStore({ reducer: { admin: slice.reducer }, devTools: false });
export const store = createAdminStore();
export type RootState = ReturnType<typeof store.getState>;
export type AppDispatch = typeof store.dispatch;
export const useAppDispatch = () => useDispatch<AppDispatch>();
export const useAppSelector: TypedUseSelectorHook<RootState> = useSelector;
configureAPI({
  csrf: () => store.getState().admin.session?.csrf || "",
  unauthenticated: () => store.dispatch(signedOut()),
});
export async function bootstrap() {
  try {
    store.dispatch(signedIn(await api.session()));
    await Promise.all([
      store.dispatch(loadCatalog()),
      store.dispatch(refreshSnapshot()),
    ]);
  } catch {
    store.dispatch(signedOut());
  }
}

import { useState } from "react";
import { InputAdornment, MenuItem, Stack, TextField } from "@mui/material";
import {
  ActionButton,
  PageHeader,
  Panel,
  UserTable,
} from "../components/common";
import { Icon } from "../components/Icons";
import { useAppSelector } from "../store";
export function Users() {
  const users = useAppSelector((s) => s.admin.snapshot)!.users;
  const [search, setSearch] = useState(""),
    [filter, setFilter] = useState("");
  const visible = users.filter(
    (u) =>
      u.name.toLowerCase().includes(search.toLowerCase()) &&
      (!filter ||
        (filter === "enabled" ? u.status === "已启用" : u.status !== "已启用")),
  );
  return (
    <>
      <PageHeader
        title="用户与设备"
        description="集中管理配额、访问规则和设备节点。"
        actions={
          <>
            <ActionButton id="user.batch" />
            <ActionButton id="user.clone" />
            <ActionButton id="user.add" variant="contained" />
          </>
        }
      />
      <Panel
        title="全部用户"
        description={`${users.length} 位用户`}
        actions={
          <Stack className="search-row" direction="row" gap={1}>
            <TextField
              placeholder="搜索用户名"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              inputProps={{ "aria-label": "搜索用户" }}
              InputProps={{
                startAdornment: (
                  <InputAdornment position="start">
                    <Icon name="search" />
                  </InputAdornment>
                ),
              }}
            />
            <TextField
              select
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              SelectProps={{
                displayEmpty: true,
                inputProps: { "aria-label": "筛选状态" },
              }}
              sx={{ minWidth: 115 }}
            >
              <MenuItem value="">全部状态</MenuItem>
              <MenuItem value="enabled">已启用</MenuItem>
              <MenuItem value="other">其他状态</MenuItem>
            </TextField>
          </Stack>
        }
      >
        <UserTable users={visible} />
      </Panel>
    </>
  );
}

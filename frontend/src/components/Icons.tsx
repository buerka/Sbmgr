import Home from "@mui/icons-material/HomeOutlined";
import People from "@mui/icons-material/PeopleOutline";
import Route from "@mui/icons-material/AltRouteOutlined";
import Link from "@mui/icons-material/LinkOutlined";
import Settings from "@mui/icons-material/SettingsOutlined";
import Server from "@mui/icons-material/DnsOutlined";
import Backup from "@mui/icons-material/BackupOutlined";
import Shield from "@mui/icons-material/VerifiedUserOutlined";
import Health from "@mui/icons-material/MonitorHeartOutlined";
import Add from "@mui/icons-material/Add";
import More from "@mui/icons-material/MoreHoriz";
import Chevron from "@mui/icons-material/KeyboardArrowDown";
import Next from "@mui/icons-material/ChevronRight";
import Back from "@mui/icons-material/ArrowBack";
import Refresh from "@mui/icons-material/Refresh";
import Logout from "@mui/icons-material/Logout";
import Menu from "@mui/icons-material/Menu";
import Close from "@mui/icons-material/Close";
import Check from "@mui/icons-material/CheckCircleOutline";
import Search from "@mui/icons-material/Search";
import Download from "@mui/icons-material/South";
import Traffic from "@mui/icons-material/SwapVert";
import Device from "@mui/icons-material/DevicesOutlined";
import Document from "@mui/icons-material/DescriptionOutlined";
import Edit from "@mui/icons-material/EditOutlined";
import Copy from "@mui/icons-material/ContentCopyOutlined";
import Warning from "@mui/icons-material/WarningAmberOutlined";
import type { SvgIconProps } from "@mui/material";
const icons = {
  home: Home,
  users: People,
  routes: Route,
  link: Link,
  settings: Settings,
  server: Server,
  backup: Backup,
  shield: Shield,
  health: Health,
  add: Add,
  more: More,
  chevron: Chevron,
  next: Next,
  back: Back,
  refresh: Refresh,
  logout: Logout,
  menu: Menu,
  close: Close,
  check: Check,
  search: Search,
  download: Download,
  traffic: Traffic,
  device: Device,
  document: Document,
  edit: Edit,
  copy: Copy,
  warning: Warning,
};
export type IconName = keyof typeof icons;
export function Icon({ name, ...props }: SvgIconProps & { name: IconName }) {
  const Component = icons[name];
  return <Component fontSize="small" {...props} />;
}

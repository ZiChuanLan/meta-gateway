import { Drawer } from "../../components/Drawer";
import type { ThemeDetailsProps } from "../types";

export function ModernDetails({ open, onClose, title, children }: ThemeDetailsProps) {
  return open ? <Drawer title={title} onClose={onClose} width={560} className="channel-inspector">{children}</Drawer> : null;
}

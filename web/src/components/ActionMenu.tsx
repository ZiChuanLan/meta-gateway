import { ChevronDown, MoreHorizontal } from "lucide-react";
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Button } from "./ui";
import { focusAfterPopover } from "./overlayFocus";
import { registerOverlay } from "./overlayStack";

export type ActionMenuItem = {
  key: string;
  label: string;
  icon?: ReactNode;
  group?: string;
  danger?: boolean;
  disabled?: boolean;
  disabledReason?: string;
  onSelect: () => void;
};

type MenuPosition = { top: number; left: number };

export function ActionMenu({
  label, title, disabled, items, compact = false,
  open: openControlled, onOpenChange, position,
}: {
  label: string;
  title?: string;
  disabled?: boolean;
  items: ActionMenuItem[];
  compact?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  position?: MenuPosition;
}) {
  const [localOpen, setLocalOpen] = useState(false);
  const open = (openControlled ?? localOpen) && items.length > 0;
  const isContext = Boolean(position);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const restoreFocus = useRef(true);
  const initialFocus = useRef<"first" | "last">("first");
  const search = useRef({ text: "", at: 0 });
  const menuId = useId();
  const [panelStyle, setPanelStyle] = useState<CSSProperties>({ visibility: "hidden" });
  const setOpen = (value: boolean) => {
    onOpenChange?.(value);
    if (openControlled === undefined) setLocalOpen(value);
  };
  const close = (restore = true) => {
    restoreFocus.current = restore;
    setOpen(false);
  };
  const closeRef = useRef(close);
  closeRef.current = close;

  const groups = useMemo(() => {
    const grouped = new Map<string, { key: string; label?: string; danger: boolean; items: ActionMenuItem[] }>();
    for (const item of items) {
      const key = item.danger ? "__danger__" : item.group ?? "";
      if (!grouped.has(key)) grouped.set(key, { key, label: item.group, danger: !!item.danger, items: [] });
      grouped.get(key)!.items.push(item);
    }
    return [...grouped.values()].sort((a, b) => Number(a.danger) - Number(b.danger));
  }, [items]);

  const place = useCallback(() => {
    const panel = panelRef.current;
    if (!panel) return;
    const padding = 8;
    const viewportWidth = document.documentElement.clientWidth || window.innerWidth;
    const availableWidth = Math.max(0, viewportWidth - padding * 2);
    const availableHeight = Math.max(0, window.innerHeight - padding * 2);
    // Entrance transforms shrink getBoundingClientRect() temporarily. Use
    // layout dimensions so a menu opened at the viewport edge stays inside
    // the viewport after its animation finishes.
    const width = Math.min(panel.offsetWidth || 256, availableWidth);
    const height = Math.min(panel.offsetHeight || 240, availableHeight, 520);
    let left = position?.left ?? 0;
    let top = position?.top ?? 0;
    if (!isContext) {
      const anchor = triggerRef.current?.getBoundingClientRect();
      if (!anchor) return;
      left = anchor.right - width;
      top = anchor.bottom + 6;
      if (top + height > window.innerHeight - padding) top = anchor.top - height - 6;
    }
    left = Math.max(padding, Math.min(left, viewportWidth - width - padding));
    top = Math.max(padding, Math.min(top, window.innerHeight - height - padding));
    const next: CSSProperties = {
      top, left, maxWidth: Math.min(320, availableWidth),
      minWidth: Math.min(224, availableWidth), maxHeight: Math.min(520, availableHeight),
    };
    setPanelStyle((current) => current.top === top && current.left === left &&
      current.maxHeight === next.maxHeight && current.maxWidth === next.maxWidth && !current.visibility ? current : next);
  }, [isContext, position?.left, position?.top]);

  useLayoutEffect(() => {
    if (!open) {
      setPanelStyle({ visibility: "hidden" });
      return;
    }
    place();
    const frame = requestAnimationFrame(place);
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(place);
    if (panelRef.current) observer?.observe(panelRef.current);
    return () => { cancelAnimationFrame(frame); observer?.disconnect(); };
  }, [open, place, items, title]);

  useEffect(() => {
    const panel = panelRef.current;
    if (!open || !panel) return;
    returnFocus.current = triggerRef.current ??
      (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    restoreFocus.current = true;
    search.current = { text: "", at: 0 };
    const unregister = registerOverlay(() => closeRef.current(), {
      modal: false, transient: true, element: panel,
    });
    const options = panel.querySelectorAll<HTMLElement>('[role="menuitem"]');
    (initialFocus.current === "last" ? options[options.length - 1] : options[0])?.focus({ preventScroll: true });
    const onPointer = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (target && (rootRef.current?.contains(target) || panel.contains(target))) return;
      closeRef.current(false);
    };
    const onViewport = (event: Event) => {
      if (event.target instanceof Node && panel.contains(event.target)) return;
      if (isContext) closeRef.current(false);
      else place();
    };
    window.addEventListener("pointerdown", onPointer, true);
    window.addEventListener("scroll", onViewport, true);
    window.addEventListener("resize", onViewport);
    return () => {
      unregister();
      window.removeEventListener("pointerdown", onPointer, true);
      window.removeEventListener("scroll", onViewport, true);
      window.removeEventListener("resize", onViewport);
      const previous = returnFocus.current;
      if (restoreFocus.current && previous?.isConnected &&
        (panel.contains(document.activeElement) || document.activeElement === document.body)) {
        previous.focus({ preventScroll: true });
      }
    };
  }, [open, isContext, place]);

  const onMenuKey = (event: KeyboardEvent<HTMLDivElement>) => {
    const options = Array.from(panelRef.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? []);
    const current = options.indexOf(document.activeElement as HTMLElement);
    let next: HTMLElement | undefined;
    switch (event.key) {
      case "ArrowDown": next = options[(current + 1) % options.length]; break;
      case "ArrowUp": next = options[(current - 1 + options.length) % options.length]; break;
      case "Home": next = options[0]; break;
      case "End": next = options[options.length - 1]; break;
      case "Tab": {
        event.preventDefault(); event.stopPropagation();
        const anchor = triggerRef.current ?? returnFocus.current;
        close(false);
        focusAfterPopover(anchor, panelRef.current, event.shiftKey);
        return;
      }
      default:
        if (event.nativeEvent.isComposing || event.key.length !== 1 || event.key === " " || event.ctrlKey || event.metaKey || event.altKey) return;
        search.current = {
          text: (Date.now() - search.current.at < 600 ? search.current.text : "") + event.key.toLocaleLowerCase(),
          at: Date.now(),
        };
        next = [...options.slice(current + 1), ...options.slice(0, current + 1)]
          .find((element) => element.dataset.label?.toLocaleLowerCase().startsWith(search.current.text));
    }
    if (next) {
      event.preventDefault(); event.stopPropagation();
      next.focus({ preventScroll: true });
      next.scrollIntoView?.({ block: "nearest", inline: "nearest" });
    }
  };

  const panel = open ? createPortal(
    <div ref={panelRef} id={menuId} role="menu" aria-label={title ?? label} tabIndex={-1}
      className={"action-menu-panel action-menu-panel-floating" + (isContext ? " action-menu-panel-context" : "")}
      style={panelStyle} onKeyDown={onMenuKey}
      onClick={(event) => event.stopPropagation()}
      onContextMenu={(event) => { event.preventDefault(); event.stopPropagation(); }}>
      {title ? <div className="action-menu-title" title={title}>{title}</div> : null}
      {groups.map((group, groupIndex) => <div key={group.key} role="group" aria-label={group.label}>
        {groupIndex > 0 ? <div className="action-menu-separator" role="separator" /> : null}
        {group.label && !(group.items.length === 1 && group.items[0]?.label === group.label)
          ? <div className="action-menu-group-label">{group.label}</div> : null}
        {group.items.map((item) => {
          const inactive = Boolean(disabled || item.disabled);
          const id = menuId + "-" + item.key;
          const hint = inactive ? item.disabledReason : undefined;
          return <button key={item.key} type="button" role="menuitem" tabIndex={-1}
            data-label={item.label} aria-disabled={inactive || undefined}
            aria-labelledby={id + "-label"} aria-describedby={hint ? id + "-hint" : undefined}
            className={"action-menu-item" + (item.danger ? " is-danger" : "")}
            onPointerMove={(event) => { if (event.pointerType === "mouse") event.currentTarget.focus({ preventScroll: true }); }}
            onClick={() => { if (inactive) return; close(); item.onSelect(); }}>
            <span className="action-menu-icon" aria-hidden="true">{item.icon}</span>
            <span className="action-menu-copy">
              <span id={id + "-label"}>{item.label}</span>
              {hint ? <small id={id + "-hint"}>{hint}</small> : null}
            </span>
          </button>;
        })}
      </div>)}
    </div>, document.body,
  ) : null;

  if (isContext) return panel;
  const triggerProps = {
    ref: triggerRef,
    disabled: disabled || items.length === 0,
    "aria-expanded": open,
    "aria-haspopup": "menu" as const,
    "aria-controls": open ? menuId : undefined,
    onClick: (event: React.MouseEvent<HTMLButtonElement>) => {
      event.stopPropagation(); initialFocus.current = "first"; setOpen(!open);
    },
    onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => {
      if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
      event.preventDefault(); event.stopPropagation();
      initialFocus.current = event.key === "ArrowUp" ? "last" : "first"; setOpen(true);
    },
  };
  return <div className="action-menu" ref={rootRef}>
    {compact ? <button {...triggerProps} type="button" className="icon-button action-menu-trigger" aria-label={label} title={label}><MoreHorizontal size={16} /></button>
      : <Button {...triggerProps} variant="secondary" icon={<MoreHorizontal size={14} />}>{label}<ChevronDown size={14} /></Button>}
    {panel}
  </div>;
}

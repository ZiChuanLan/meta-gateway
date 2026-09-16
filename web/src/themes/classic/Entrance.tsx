import type { EntrancePhase } from "../types";

/** The original sliding doors, kept inside the shared accessible transition host. */
export function ClassicEntrance({ phase }: { phase: EntrancePhase }) {
  if (phase === "sheathing") return <><div className="sheath-veil" /><div className="sheath-blade" /><div className="sheath-point" /><div className="sheath-word"><span>SESSION SEALED</span><small>META GATEWAY</small></div></>;
  return <>
    <div className="gateway-plane"><div className="gateway-plane-line gateway-plane-line-a" /><div className="gateway-plane-line gateway-plane-line-b" /><div className="gateway-plane-line gateway-plane-line-c" /><div className="gateway-plane-line gateway-plane-line-d" /></div>
    <div className="gateway-doors"><div className="gateway-door gateway-door-left"><span>ADMIN / AUTH</span><strong>TOKEN</strong><small>BEARER VERIFIED</small></div><div className="gateway-door gateway-door-right"><span>RELAY / ROUTING</span><strong>SITES</strong><small>CONSOLE OPENING</small></div></div>
    <div className="gateway-console-stage"><div className="gateway-transition-lock"><span>ADMIN SESSION ESTABLISHED</span><strong>CONSOLE<br />ONLINE</strong><div><b>OK</b><i>API</i></div><small>SITES · MODELS · KEYS · AUDIT</small></div></div>
    <div className="gateway-transition-axis" />
  </>;
}

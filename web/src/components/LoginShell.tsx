import type { CSSProperties, ReactNode, Ref } from "react";
import { BrandMark } from "./BrandMark";

/**
 * The sign-in shell: one page for both front doors.
 *
 * The console and the member app had two different sign-in pages — the console's
 * a composed stage with the mark, the sculpture and the light ribbons; the
 * member app's a plain card. They are the same product, so they are now the same
 * page: this shell owns the atmosphere, the brand line, the introduction and the
 * footer, and each app supplies only what differs — the fields inside the card
 * and the controls in the top-right corner.
 *
 * This component is visual only. The unified console owns authentication,
 * TOTP, background preferences and the same entrance sequence for every role.
 */
export function LoginShell({
  tools,
  edition,
  title,
  description,
  children,
  cardFocused = false,
  footerLeft = "Meta Gateway",
  footerRight,
  rootRef,
  className,
  effects,
  atmosphereStyle,
}: {
  /** Top-right controls (language, theme, background). */
  tools?: ReactNode;
  /** The small label above the headline, e.g. "Workspace". */
  edition: string;
  /** The headline; a second line may be wrapped in a <span> for the accent. */
  title: ReactNode;
  description: string;
  /** The card: a `<section className="login-card">…</section>`. */
  children: ReactNode;
  /** Deepens the card's shadow while a field has focus. */
  cardFocused?: boolean;
  footerLeft?: string;
  footerRight?: string;
  /** The page element, for a host that tracks the pointer across it. */
  rootRef?: Ref<HTMLDivElement>;
  /** Extra classes on the page (the console adds its leaving state). */
  className?: string;
  /** Decoration painted above the atmosphere — the console's canvas. */
  effects?: ReactNode;
  /** Inline style for the atmosphere (the console's background image). */
  atmosphereStyle?: CSSProperties;
}) {
  return (
    <div ref={rootRef} className={"login-page login-motion" + (className ? " " + className : "")}>
      <div className="login-atmosphere" aria-hidden="true" style={atmosphereStyle} />
      {effects}
      <div className="login-light-ribbons" aria-hidden="true">
        <i />
        <i />
        <i />
      </div>
      <header className="login-header">
        <div className="login-brand">
          <span className="console-brand-mark">
            <BrandMark size={23} />
          </span>
          <strong>Meta Gateway</strong>
        </div>
        <div className="login-tools">{tools}</div>
      </header>
      <main className="login-stage">
        <section className="login-introduction">
          <span className="login-edition">{edition}</span>
          <h1>{title}</h1>
          <p>{description}</p>
          <div className="login-sculpture" aria-hidden="true">
            <span className="login-poster-code">01 / CONNECT</span>
            <span className="login-orbit" />
            <span className="login-tile is-back" />
            <span className="login-tile is-middle" />
            <span className="login-tile is-front">
              <BrandMark size={46} />
            </span>
            <span className="login-orbit-point" />
            <span className="login-poster-axis">META / GATEWAY</span>
          </div>
        </section>
        <div className={cardFocused ? "login-card-slot is-focused" : "login-card-slot"}>
          {children}
        </div>
      </main>
      <footer className="login-footer">
        <span>{footerLeft}</span>
        {footerRight ? <span>{footerRight}</span> : null}
      </footer>
    </div>
  );
}

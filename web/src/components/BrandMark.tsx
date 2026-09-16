/**
 * Brand mark: a near-black tile with a light-blue square inset in the middle.
 *
 * Deliberately hard-coded rather than token-driven — it is the product's
 * identity, so it stays the same across both appearances and both colour
 * schemes. Sizes are expressed through width/height so the mark drops straight
 * into the existing 18px / 23px / 48px slots.
 */
export function BrandMark({
	size = 24,
	className,
}: {
	size?: number;
	className?: string;
}) {
	return (
		<svg
			className={className}
			width={size}
			height={size}
			viewBox="0 0 32 32"
			role="img"
			aria-label="Meta Gateway"
			focusable="false"
		>
			<rect width="32" height="32" fill="#090d18" />
			<rect x="9" y="9" width="14" height="14" fill="#8fc2ec" />
		</svg>
	);
}

export default BrandMark;

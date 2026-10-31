import type { ComponentProps } from "react";
import type { QRCode as LiveQRCode } from "@/components/atoms/QRCode";
import { cn } from "@/lib/cn";

// The demo's QR code (vite.demo.config.ts puts it in place of
// components/atoms/QRCode): a square that says why it is empty, as there is
// no device to sign in, so the demo ships no QR encoder.
export function QRCode({ label, className }: ComponentProps<typeof LiveQRCode>) {
  return (
    <div
      role="img"
      aria-label={label}
      className={cn(
        "flex items-center justify-center rounded-sm border border-divider bg-surface-1 p-2",
        "text-center text-[12px] text-ink-muted",
        className,
      )}
    >
      No QR code in the demo
    </div>
  );
}
